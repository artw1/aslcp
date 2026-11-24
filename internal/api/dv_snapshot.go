package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/hostlists"
	"github.com/artw1/aslcp/internal/nowcalling"
)

// The digital modes snapshot: what the dashboard card shows, safe for every
// logged-in role (docs/DV.md, "API"). It holds no secret, no install log and
// no identity of the node; it is built from explicit fields only. The one
// personal part is the callers (callsigns and DMR IDs of whoever talks on the
// bridge), as heard on air; every role of the dashboard login sees it, there
// is no unauthenticated stream.

// DVSnapshot is the reply of GET /api/dv/snapshot and the payload of the
// SSE event "dv".
type DVSnapshot struct {
	// Installed: digital modes are enabled on this node. Running: the engine
	// answers. Configured: its config file exists.
	Installed  bool `json:"installed"`
	Running    bool `json:"running"`
	Configured bool `json:"configured"`
	// Bridge is the live state of the engine.
	Bridge   DVBridge        `json:"bridge"`
	Modes    []DVModeView    `json:"modes"`
	Services []DVServiceView `json:"services"`
	// Vocoder and Engine are absent while the engine does not answer.
	Vocoder    *DVVocoderView `json:"vocoder,omitempty"`
	Engine     *DVEngineView  `json:"engine,omitempty"`
	BridgeNode DVBridgeNode   `json:"bridgeNode"`
	// Link is the link of the bridge node to a local node.
	Link DVLink `json:"link"`
	// Caller is who is talking now (the panel shows it only while RX is
	// keyed). Heard is the last few distinct callers, newest first. Logged-in
	// roles only.
	Caller *dvpanel.Caller  `json:"caller,omitempty"`
	Heard  []dvpanel.Caller `json:"heard"`
}

// DVBridge: Mode is a panel mode (dvpanel.PanelModes; "" when none is set);
// Tune is the target as text (empty for LOOP, the echo test; the talkgroup for a DMR
// network, the reflector id or host:port for YSF); Slot is the DMR slot (0
// when unknown); Source is "aslcp-dv" or "none".
type DVBridge struct {
	Mode       string `json:"mode"`
	Tune       string `json:"tune"`
	TargetName string `json:"targetName,omitempty"`
	Slot       int    `json:"slot"`
	Muted      bool   `json:"muted"`
	// Private: the target is a DMR private call (Tune is then the ID that
	// was called, not a talkgroup the node is tuned to).
	Private bool   `json:"private,omitempty"`
	Source  string `json:"source"`
	Updated string `json:"updated"`
}

// DVModeView: Enabled is the mode's switch in the engine's config, Available
// says the build implements the mode (from the engine; the static dvpanel.NativeModes while it is down), Unlink is
// the tune value the unlink button sends. Label names a DMR network's button.
type DVModeView struct {
	ID        string `json:"id"`
	Enabled   bool   `json:"enabled"`
	Available bool   `json:"available"`
	Unlink    string `json:"unlink"`
	Label     string `json:"label,omitempty"`
	// Login and Detail are the engine's login state of an enabled mode.
	Login  string `json:"login,omitempty"`
	Detail string `json:"detail,omitempty"`
	// Silent: the YSF, P25, NXDN or M17 reflector of the tuned target does not answer
	// (the engine's lost state, ctl.NotAnswering); the dashboard says so.
	Silent bool `json:"silent,omitempty"`
	// QuietSince (DMR - Other only, RFC 3339) is the later of the tune of the
	// current talkgroup and the last call on it; absent while a call is in
	// progress or when no talkgroup is tuned. The web shows its "no traffic"
	// hint when it is 15 minutes old (DMR+ masters drop an idle key-up TG).
	QuietSince string `json:"quietSince,omitempty"`
	// Master (BM and DMR - Other only) is the master the mode is set to: its
	// name in the host list when the saved address is on it, else the address.
	Master string `json:"master,omitempty"`
}

// DVServiceView is one systemd unit of the digital modes stack.
type DVServiceView struct {
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}

// DVVocoderView is the md380 vocoder's health.
type DVVocoderView struct {
	OK       bool   `json:"ok"`
	Timeouts uint64 `json:"timeouts"`
}

// DVEngineView is the engine's version and uptime in seconds.
type DVEngineView struct {
	Version string `json:"version"`
	Uptime  int64  `json:"uptime"`
}

// DVBridgeNode says whether a local node uses a USRP channel (the private
// bridge node); read-only detection from rpt.conf.
type DVBridgeNode struct {
	Present bool   `json:"present"`
	Node    string `json:"node,omitempty"`
	// Name is the display name (never empty when Present: the default
	// "Digital modes linked node" when none was set).
	Name string `json:"name,omitempty"`
}

const (
	dvStatusTTL = 15 * time.Second
	dvNodeTTL   = 60 * time.Second
	// dvRing is how many distinct callers and talks are kept.
	dvRing = 10
)

// Units of the digital modes stack, in the order of the services list.
const (
	dvEngineUnit = "aslcp-dv"
	dvVocoder    = "md380-emu"
)

var dvModeLabels = map[string]string{ctl.ModeLoop: ctl.EchoTestName, ctl.ModeBM: "BrandMeister", ctl.ModeTGIF: "TGIF", ctl.ModeDMR: "DMR - Other"}

// dvSnapper keeps the engine's state (from its event stream), builds
// snapshots and publishes the changes.
type dvSnapper struct {
	s   *Server
	now func() time.Time

	mu        sync.Mutex
	st        ctl.Status
	connected bool
	talk      *ctl.Talk  // the call in progress
	talks     []ctl.Talk // newest first, at most dvRing, the call in progress first
	heard     []dvpanel.Caller
	key       string
	// quietKey identifies the tuned DMR - Other talkgroup ("" when none:
	// other mode, private call, unlinked); quietAt is when it last had traffic.
	quietKey string
	quietAt  time.Time

	hstatus dvCache[helper.DVStatus]
	node    dvCache[DVBridgeNode]
	masters dvCache[map[string]string] // mode -> master label
	// masterMemo keeps the label of each mode for its address and list tag, so
	// the list is searched again only when one of them changes.
	masterMemo map[string]masterMemo
	// configPath is the engine's config file (tests point it elsewhere).
	configPath string
}

type dvCache[T any] struct {
	v  T
	ok bool
	at time.Time
}

func (c *dvCache[T]) fresh(now time.Time, ttl time.Duration) bool {
	return !c.at.IsZero() && now.Sub(c.at) < ttl
}

func newDVSnapper(s *Server) *dvSnapper {
	return &dvSnapper{s: s, now: time.Now, configPath: ctl.ConfigPath}
}

// DVEnabled says whether digital modes are on: the hook when one is set,
// else the helper's report (cached for a few seconds) or a live engine.
func (s *Server) DVEnabled() bool { return s.dvEnabled() }

func (s *Server) dvEnabled() bool {
	if s.d.DVEngine == nil && s.d.DVCtl == nil {
		return false
	}
	if s.d.DVEnabled != nil {
		return s.d.DVEnabled()
	}
	if s.dvs == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if hs, ok := s.dvs.helperStatus(ctx); ok && hs.Enabled {
		return true
	}
	s.dvs.mu.Lock()
	defer s.dvs.mu.Unlock()
	return s.dvs.connected
}

// invalidate drops the cached helper status and bridge node (after enable,
// disable or a change of the config).
func (d *dvSnapper) invalidate() {
	d.mu.Lock()
	d.hstatus, d.node = dvCache[helper.DVStatus]{}, dvCache[DVBridgeNode]{}
	d.mu.Unlock()
}

// run keeps one subscription to the engine while digital modes are enabled:
// it reconnects with a growing pause and stops with ctx.
func (d *dvSnapper) run(ctx context.Context) {
	wait := time.Second
	for {
		if d.s.d.DVCtl == nil {
			return
		}
		if !d.s.dvEnabled() {
			d.down()
			if !dvSleep(ctx, 5*time.Second) {
				return
			}
			continue
		}
		started := d.now()
		err := d.s.d.DVCtl.Subscribe(ctx, d.first, d.event)
		d.down()
		if ctx.Err() != nil {
			return
		}
		if d.now().Sub(started) > time.Minute {
			wait = time.Second
		}
		if err != nil {
			var ce *ctl.Error
			if errors.As(err, &ce) {
				d.s.d.Logger.Printf("dv: engine refused the subscription: %v", err)
			}
		}
		if !dvSleep(ctx, wait) {
			return
		}
		if wait *= 2; wait > 15*time.Second {
			wait = 15 * time.Second
		}
	}
}

func dvSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// first is the initial status of a subscription.
func (d *dvSnapper) first(st ctl.Status) {
	d.mu.Lock()
	d.connected = true
	d.applyLocked(st)
	d.mu.Unlock()
	d.refresh()
}

// setStatus takes a status that a command returned.
func (d *dvSnapper) setStatus(st ctl.Status) {
	d.mu.Lock()
	d.connected = true
	d.applyLocked(st)
	d.mu.Unlock()
}

// quietKeyOf is the identity of the DMR - Other talkgroup a status tunes, ""
// when there is none. The engine's Updated time is part of it, so a retune of
// the same talkgroup counts as a new tune.
func quietKeyOf(st ctl.Status) string {
	if st.Mode != ctl.ModeDMR || st.Target == nil || st.Target.TG == 0 || st.Target.Private {
		return ""
	}
	return strconv.FormatUint(uint64(st.Target.TG), 10) + "|" + strconv.FormatInt(st.Updated.UnixNano(), 10)
}

func (d *dvSnapper) applyLocked(st ctl.Status) {
	d.st = st
	if k := quietKeyOf(st); k != d.quietKey {
		d.quietKey = k
		d.quietAt = time.Time{}
		if k != "" {
			d.quietAt = d.now()
		}
	}
	if st.Talk != nil {
		d.noteTalkLocked(*st.Talk)
	} else if d.talk != nil {
		d.endTalkLocked(*d.talk, d.now())
	}
}

// event folds one engine event.
func (d *dvSnapper) event(ev ctl.Event) {
	d.mu.Lock()
	switch ev.Type {
	case ctl.EvStatus:
		if ev.Status != nil {
			d.applyLocked(*ev.Status)
		}
	case ctl.EvTalkStart:
		if ev.Talk != nil {
			d.noteTalkLocked(*ev.Talk)
		}
	case ctl.EvTalkEnd:
		if ev.Talk != nil {
			d.endTalkLocked(*ev.Talk, ev.At)
		}
	case ctl.EvVocoder:
		if ev.Vocoder != nil {
			d.st.Vocoder = *ev.Vocoder
		}
	case ctl.EvLogin:
		if ev.Login != nil {
			for i := range d.st.Networks {
				if d.st.Networks[i].Mode == ev.Login.Mode {
					d.st.Networks[i].Login, d.st.Networks[i].Detail = ev.Login.State, ev.Login.Detail
				}
			}
		}
	default:
		d.mu.Unlock()
		return // ping and anything unknown: no change
	}
	d.mu.Unlock()
	d.refresh()
}

// down marks the engine as gone and closes the call in progress.
func (d *dvSnapper) down() {
	d.mu.Lock()
	was := d.connected
	d.connected = false
	if d.talk != nil {
		d.endTalkLocked(*d.talk, d.now())
	}
	d.st = ctl.Status{}
	d.quietKey, d.quietAt = "", time.Time{}
	d.mu.Unlock()
	if was {
		d.refresh()
	}
}

func (d *dvSnapper) noteTalkLocked(t ctl.Talk) {
	d.talk = &t
	d.quietCallLocked(t)
	for i := range d.talks {
		if d.talks[i].ID == t.ID {
			d.talks[i] = t
			return
		}
	}
	d.talks = append([]ctl.Talk{t}, d.talks...)
	if len(d.talks) > dvRing {
		d.talks = d.talks[:dvRing]
	}
}

// quietCallLocked restarts the quiet time on a call of DMR - Other (a call
// starting or ending, received or sent).
func (d *dvSnapper) quietCallLocked(t ctl.Talk) {
	if d.quietKey == "" || t.Mode != ctl.ModeDMR {
		return
	}
	d.quietAt = d.now()
	if !t.End.IsZero() {
		d.quietAt = t.End
	}
}

func (d *dvSnapper) endTalkLocked(t ctl.Talk, at time.Time) {
	if t.End.IsZero() {
		if at.IsZero() {
			at = d.now()
		}
		t.End = at
	}
	if d.talk != nil && d.talk.ID == t.ID {
		d.talk = nil
	}
	d.noteTalkLocked(t)
	d.talk = nil
	d.quietCallLocked(t) // after noteTalkLocked: the end time counts
	c := d.caller(t)
	out := []dvpanel.Caller{c}
	for _, h := range d.heard {
		if h.Call == c.Call && h.DMRID == c.DMRID && h.Private == c.Private && h.Dst == c.Dst {
			continue
		}
		out = append(out, h)
	}
	if len(out) > dvRing {
		out = out[:dvRing]
	}
	d.heard = out
}

// dvCallsign is the callsign of a talk. A Homebrew master sends no callsign:
// the engine puts the DMR ID in its place, and an all-digit call on a DMR
// network is that ID, not a callsign. It counts as none ("" here; the radioid.net
// list may fill it in, and the web shows the DMR ID when it does not).
func dvCallsign(t ctl.Talk) string {
	if dvpanel.DMRFamily(t.Mode) && allDigits(t.Call) {
		return ""
	}
	return t.Call
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// caller is dvCaller plus the callsign of the radioid.net entry of the DMR ID
// when the talk carries none.
func (d *dvSnapper) caller(t ctl.Talk) dvpanel.Caller {
	c := dvCaller(t)
	if c.Call == "" && !c.Private && t.DMRID != 0 && dvpanel.DMRFamily(t.Mode) && d.s.d.RadioID != nil {
		if u, ok := d.s.d.RadioID.ByID(t.DMRID); ok {
			c.Call = u.Callsign
		}
	}
	return c
}

// dvCaller is a call as the panel shows it.
func dvCaller(t ctl.Talk) dvpanel.Caller {
	c := dvpanel.Caller{Call: dvCallsign(t), Private: t.Private, Slot: t.Slot, At: t.Start.Local().Format("2006-01-02T15:04:05")}
	if ctl.IsEchoTest(t) {
		c.Call = ctl.EchoTestName
	}
	if t.DMRID != 0 {
		c.DMRID = strconv.FormatUint(uint64(t.DMRID), 10)
	}
	switch {
	case t.TG != 0:
		c.Dst = strconv.FormatUint(uint64(t.TG), 10)
	case t.Room != "":
		c.Dst = t.Room
	}
	return c
}

// info is the engine's state in the panel's terms.
func (d *dvSnapper) info() dvpanel.Info {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.connected {
		return dvpanel.Info{}
	}
	return dvpanel.InfoFromStatus(d.st)
}

// snapshot builds the current snapshot. It reads the helper's status (cached)
// and the engine's state as the stream last left it.
func (d *dvSnapper) snapshot(ctx context.Context) DVSnapshot {
	sn := DVSnapshot{Bridge: DVBridge{Source: "none"}, Modes: []DVModeView{}, Services: []DVServiceView{}, Heard: []dvpanel.Caller{}, Link: DVLink{Mode: dvpanel.LinkOnDemand}}
	if !d.s.dvEnabled() {
		return sn
	}
	hs, hok := d.helperStatus(ctx)
	d.mu.Lock()
	connected, st := d.connected, d.st
	st.Networks = append([]ctl.NetStatus(nil), d.st.Networks...) // own copy: event() updates entries in place
	var talk *ctl.Talk
	if d.talk != nil {
		t := *d.talk
		talk = &t
	}
	heard := append([]dvpanel.Caller(nil), d.heard...)
	quiet := time.Time{}
	if d.quietKey != "" && d.talk == nil {
		quiet = d.quietAt
	}
	d.mu.Unlock()

	sn.Installed = true
	sn.Running = connected
	sn.Configured = connected
	if !sn.Configured {
		_, err := os.Stat(d.configPath)
		sn.Configured = err == nil
	}
	nets := map[string]ctl.NetStatus{}
	for _, n := range st.Networks {
		nets[n.Mode] = n
	}
	for _, m := range dvpanel.PanelModes {
		mv := DVModeView{ID: m, Unlink: dvpanel.UnlinkTune(m), Label: dvModeLabels[m]}
		if n, ok := nets[m]; ok && connected {
			mv.Enabled, mv.Available = n.Enabled, n.Available
			if n.Enabled && n.Available {
				mv.Login, mv.Detail = string(n.Login), n.Detail
				mv.Silent = ctl.NotAnswering(n.Login, n.Detail)
			}
			if m == dvpanel.ModeDMR && !quiet.IsZero() {
				mv.QuietSince = quiet.UTC().Format(time.RFC3339)
			}
		} else {
			mv.Enabled, mv.Available = m == dvpanel.ModeLoop, dvpanel.NativeMode(m)
		}
		sn.Modes = append(sn.Modes, mv)
	}
	engineActive, vocoderActive := connected, connected && st.Vocoder.OK
	if hok {
		engineActive, vocoderActive = engineActive || hs.Active, vocoderActive || hs.MD380Active
	}
	sn.Services = []DVServiceView{{Unit: dvEngineUnit, Active: engineActive}, {Unit: dvVocoder, Active: vocoderActive}}
	if connected {
		sn.Vocoder = &DVVocoderView{OK: st.Vocoder.OK, Timeouts: st.Vocoder.Timeouts}
		up := int64(0)
		if !st.Started.IsZero() {
			up = int64(d.now().Sub(st.Started) / time.Second)
			if up < 0 {
				up = 0
			}
		}
		sn.Engine = &DVEngineView{Version: st.Version, Uptime: up}
		d.fillMasters(ctx, sn.Modes)
		d.fillBridge(&sn, st)
		if talk != nil {
			c := d.caller(*talk)
			sn.Caller = &c
		}
		sn.Heard = heard
	}
	sn.BridgeNode = d.getBridgeNode(ctx)
	if sn.BridgeNode.Present {
		// read on every build (not cached with the node): a rename shows at once
		sn.BridgeNode.Name = d.s.dvNodeName()
	}
	sn.Link = d.s.dvLinkView(sn.BridgeNode)
	return sn
}

func (d *dvSnapper) fillBridge(sn *DVSnapshot, st ctl.Status) {
	in := dvpanel.InfoFromStatus(st)
	b := &sn.Bridge
	b.Source, b.Updated, b.Mode, b.Tune, b.Private = "aslcp-dv", in.Updated, in.Mode, in.Tune, in.Private
	b.Slot, _ = strconv.Atoi(in.Slot)
	if b.Mode == dvpanel.ModeLoop {
		// the echo test has no target: nothing is "tuned" (the engine reports the
		// parrot id of the old tune API)
		b.Private, b.Tune = false, ""
	}
	if b.Tune != "" {
		b.TargetName = d.targetName(st, b.Mode, b.Tune)
	}
}

func (d *dvSnapper) helperStatus(ctx context.Context) (helper.DVStatus, bool) {
	if d.s.d.DVEngine == nil {
		return helper.DVStatus{}, false
	}
	now := d.now()
	d.mu.Lock()
	if d.hstatus.fresh(now, dvStatusTTL) {
		v, ok := d.hstatus.v, d.hstatus.ok
		d.mu.Unlock()
		return v, ok
	}
	d.mu.Unlock()
	v, err := d.s.d.DVEngine.DVStatus(ctx)
	d.mu.Lock()
	defer d.mu.Unlock()
	if err != nil {
		if d.hstatus.ok { // keep the last good one for a while
			d.hstatus.at = now.Add(dvStatusTTL - 5*time.Second)
			return d.hstatus.v, true
		}
		d.hstatus = dvCache[helper.DVStatus]{at: now.Add(dvStatusTTL - 5*time.Second)}
		return v, false
	}
	d.hstatus = dvCache[helper.DVStatus]{v: v, ok: true, at: now}
	return v, true
}

func (d *dvSnapper) getBridgeNode(ctx context.Context) DVBridgeNode {
	f := d.s.d.DVBridgeNode
	if f == nil {
		return DVBridgeNode{}
	}
	now := d.now()
	d.mu.Lock()
	if d.node.fresh(now, dvNodeTTL) {
		v := d.node.v
		d.mu.Unlock()
		return v
	}
	d.mu.Unlock()
	n, ok := f(ctx)
	v := DVBridgeNode{Present: ok}
	if ok {
		v.Node = n
	}
	d.mu.Lock()
	d.node = dvCache[DVBridgeNode]{v: v, ok: true, at: now}
	d.mu.Unlock()
	return v
}

// targetName names the target: the engine's own name, the host list entry
// (YSF, P25, NXDN, M17) or a favourite. "" when unknown.
func (d *dvSnapper) targetName(st ctl.Status, mode, tune string) string {
	if st.Target != nil && st.Target.Name != "" {
		return st.Target.Name
	}
	if !dvpanel.DMRFamily(mode) && mode != dvpanel.ModeLoop && d.s.d.HostLists != nil {
		if t, ok := d.s.d.HostLists.Resolve(mode, tune); ok && t.Name != "" {
			return t.Name
		}
	}
	if d.s.d.Store != nil {
		if favs, err := d.s.d.Store.ListDVFavorites(); err == nil {
			for _, f := range favs {
				if f.Mode == mode && f.Value == tune {
					return f.Name
				}
			}
		}
	}
	return ""
}

// publish broadcasts sn when it differs from the last published one. The
// update time and the uptime are left out of the comparison: they change
// without any news.
func (d *dvSnapper) publish(sn DVSnapshot) {
	cmp := sn
	cmp.Bridge.Updated = ""
	if cmp.Engine != nil {
		e := *cmp.Engine
		e.Uptime = 0
		cmp.Engine = &e
	}
	b, err := json.Marshal(cmp)
	if err != nil {
		return
	}
	d.mu.Lock()
	changed := d.key != string(b)
	d.key = string(b)
	d.mu.Unlock()
	if changed && d.s.hub.count() > 0 {
		f := frame("dv", sn)
		d.s.hub.broadcast(sseMsg{priv: f, pub: f})
	}
}

// refresh builds, publishes and feeds the "Now calling" strip.
func (d *dvSnapper) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	sn := d.snapshot(ctx)
	d.publish(sn)
	if d.s.nc != nil {
		d.mu.Lock()
		talks := append([]ctl.Talk(nil), d.talks...)
		d.mu.Unlock()
		d.s.ncDV(talks, sn)
	}
}

// refreshSoon rebuilds in the background (a favourite changed a target name).
func (d *dvSnapper) refreshSoon() {
	if d.s.d.DVCtl == nil && d.s.d.DVEngine == nil {
		return
	}
	go d.refresh()
}

// parrotID is the DMR ID of the BrandMeister parrot; its reply is a private
// call from this ID.
const parrotID = 9990

// dvNowTalks converts talks (newest first, as the snapper keeps them) for the
// "Now calling" tracker, which wants them oldest first: it takes the last one
// as the newest call.
func dvNowTalks(talks []ctl.Talk) []nowcalling.Talk {
	out := make([]nowcalling.Talk, 0, len(talks))
	for i := len(talks) - 1; i >= 0; i-- {
		t := talks[i]
		k := nowcalling.Talk{Kind: nowcalling.KindDMR, Via: nowcalling.ViaEngine, Call: dvCallsign(t), Slot: t.Slot, Private: t.Private, Start: t.Start, End: t.End, Network: dvNetworkName(t.Mode)}
		if t.Mode == ctl.ModeYSF {
			k.Kind = nowcalling.KindYSF
		}
		if t.Mode == ctl.ModeM17 {
			k.Kind = nowcalling.KindM17
		}
		if ctl.IsEchoTest(t) {
			k.Call = ctl.EchoTestName
		}
		if t.DMRID != 0 {
			k.DMRID = strconv.FormatUint(uint64(t.DMRID), 10)
		}
		switch {
		case t.Private:
			// a private call to us: the TG field would be our own ID
			if k.Call == "" && t.DMRID == parrotID {
				k.Call = "Parrot"
			}
		case t.TG != 0:
			k.Dst = strconv.FormatUint(uint64(t.TG), 10)
		case t.Room != "":
			k.Dst = t.Room
		}
		out = append(out, k)
	}
	return out
}

func dvNetworkName(mode string) string {
	switch mode {
	case ctl.ModeLoop:
		return ctl.EchoTestName
	case ctl.ModeBM:
		return "BrandMeister"
	case ctl.ModeDMR:
		return "DMR"
	}
	return mode
}

// initDV creates the snapshot builder.
func (s *Server) initDV() { s.dvs = newDVSnapper(s) }

// dvMasterName is the label of a master address: the name of the host list
// entry it matches (case-insensitive, as the setup card's masterChoice does),
// else the address as typed. "" for no address.
func dvMasterName(address string, items []hostlists.Item) string {
	a := strings.TrimSpace(address)
	if a == "" {
		return ""
	}
	for _, it := range items {
		if strings.EqualFold(it.Value, a) && it.Name != "" {
			return it.Name
		}
	}
	return a
}

type masterMemo struct{ key, name string }

// dvMasterList is the host list a mode's master is picked from (the setup
// card's lists: bm and dmr-other), from the cache; the tag changes with the
// list. Nothing is fetched.
func (d *dvSnapper) dvMasterList(mode string) ([]hostlists.Item, string) {
	if d.s.d.HostLists == nil {
		return nil, ""
	}
	l, err := d.s.d.HostLists.Get("dmr")
	if err != nil {
		return nil, ""
	}
	if mode == dvpanel.ModeBM {
		return hostlists.BMMasters(l.Items), l.ETag
	}
	return hostlists.OtherDMRMasters(l.Items), l.ETag
}

// fillMasters sets Master on the BM and DMR - Other views. The addresses come
// from the engine settings (read through the helper, cached for a few
// seconds, the last good answer is kept on an error).
func (d *dvSnapper) fillMasters(ctx context.Context, modes []DVModeView) {
	need := false
	for _, m := range modes {
		if (m.ID == dvpanel.ModeBM || m.ID == dvpanel.ModeDMR) && m.Enabled && m.Available {
			need = true
		}
	}
	if !need || d.s.d.DVEngine == nil {
		return
	}
	now := d.now()
	d.mu.Lock()
	fresh := d.masters.fresh(now, dvStatusTTL)
	d.mu.Unlock()
	if !fresh {
		rep, err := d.s.d.DVEngine.DVNetworkGet(ctx)
		d.mu.Lock()
		if err == nil {
			addr := map[string]string{}
			for _, v := range rep.Networks {
				if v.Mode == dvpanel.ModeBM || v.Mode == dvpanel.ModeDMR {
					addr[v.Mode] = v.Address
				}
			}
			if d.masterMemo == nil {
				d.masterMemo = map[string]masterMemo{}
			}
			out := map[string]string{}
			for mode, a := range addr {
				items, tag := d.dvMasterList(mode)
				key := a + "\x00" + tag
				if m, ok := d.masterMemo[mode]; ok && m.key == key {
					out[mode] = m.name
					continue
				}
				n := dvMasterName(a, items)
				d.masterMemo[mode] = masterMemo{key, n}
				out[mode] = n
			}
			d.masters = dvCache[map[string]string]{v: out, ok: true, at: now}
		} else if d.masters.ok {
			d.masters.at = now
		}
		d.mu.Unlock()
	}
	d.mu.Lock()
	cur := d.masters.v
	d.mu.Unlock()
	for i := range modes {
		if m := &modes[i]; (m.ID == dvpanel.ModeBM || m.ID == dvpanel.ModeDMR) && m.Enabled && m.Available {
			m.Master = cur[m.ID]
		}
	}
}
