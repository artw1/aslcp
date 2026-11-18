package api

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/dvswitch"
	"github.com/artw1/aslcp/internal/helper"
)

// The DVSwitch snapshot: what the dashboard card shows, safe for every
// logged-in role (docs/DVSWITCH.md, "API"). It holds no secret, no install log
// and no identity of the node; it is built from explicit fields only. The one
// personal part is the callers (callsigns and DMR IDs of whoever talks on the
// bridge), as heard on air; every role of the dashboard login sees it, there
// is no unauthenticated stream.

// DVSnapshot is the reply of GET /api/dvswitch/snapshot and the payload of the
// SSE event "dvswitch".
type DVSnapshot struct {
	Installed  bool `json:"installed"`
	Running    bool `json:"running"`
	Configured bool `json:"configured"`
	// Bridge is the live state of Analog_Bridge (ABInfo).
	Bridge     DVBridge        `json:"bridge"`
	Modes      []DVModeView    `json:"modes"`
	Services   []DVServiceView `json:"services"`
	BridgeNode DVBridgeNode    `json:"bridgeNode"`
	// Link is the link of the bridge node to a local node (M2).
	Link DVLink `json:"link"`
	// Caller is who is talking now (the last call that has not ended in the
	// log; the panel shows it only while RX is keyed). Heard is the last few
	// distinct callers, newest first. Logged-in roles only.
	Caller *helper.DVCaller  `json:"caller,omitempty"`
	Heard  []helper.DVCaller `json:"heard"`
}

// DVBridge: Mode is BM, DMR, YSF, P25 or NXDN ("" when unknown); Tune is the
// TG (BM, DMR) or the last tune (host:port for YSF); Slot is the DMR slot (0 when
// unknown); Source is "abinfo" or "none".
type DVBridge struct {
	Mode       string `json:"mode"`
	Tune       string `json:"tune"`
	TargetName string `json:"targetName,omitempty"`
	Slot       int    `json:"slot"`
	Muted      bool   `json:"muted"`
	// Private: the last tune was a DMR private call (Tune is then the ID that
	// was called, not a talkgroup the bridge is tuned to).
	Private bool   `json:"private,omitempty"`
	Source  string `json:"source"`
	Updated string `json:"updated"`
}

// DVModeView: Enabled is what MMDVM_Bridge enables; Unlink is the tune value
// the unlink button sends.
type DVModeView struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
	Unlink  string `json:"unlink"`
}

// DVServiceView is one systemd unit of the bridge.
type DVServiceView struct {
	Unit   string `json:"unit"`
	Active bool   `json:"active"`
}

// DVBridgeNode says whether a local node uses a USRP channel (the private
// bridge node of M2); read-only detection from rpt.conf.
type DVBridgeNode struct {
	Present bool   `json:"present"`
	Node    string `json:"node,omitempty"`
	// Name is the display name (never empty when Present: the default
	// "DVSwitch linked node" when none was set).
	Name string `json:"name,omitempty"`
}

const (
	dvStatusTTL   = 15 * time.Second
	dvListTTL     = 10 * time.Minute
	dvListErrTTL  = time.Minute
	dvNodeTTL     = 60 * time.Second
	dvCoalesceTTL = 400 * time.Millisecond
)

// dvSnapper builds snapshots and publishes the changes. The helper's status,
// the setup (enabled modes), the host lists and the bridge node are cached;
// ABInfo is read for every snapshot (it is cheap).
type dvSnapper struct {
	s   *Server
	now func() time.Time

	mu      sync.Mutex
	last    DVSnapshot
	key     string
	have    bool
	builtAt time.Time

	status   dvCache[helper.DVSwitchStatus]
	setup    dvCache[helper.DVSwitchSetup]
	node     dvCache[DVBridgeNode]
	lists    map[string]dvList
	tuneSeen bool
	tuneVal  string
	tuneMode string
}

type dvCache[T any] struct {
	v  T
	ok bool
	at time.Time
}

func (c *dvCache[T]) fresh(now time.Time, ttl time.Duration) bool {
	return !c.at.IsZero() && now.Sub(c.at) < ttl
}

// dvList maps the values (and for YSF the host:port) of one list to names.
type dvList struct {
	names map[string]string
	at    time.Time
	ttl   time.Duration
}

func newDVSnapper(s *Server) *dvSnapper {
	return &dvSnapper{s: s, now: time.Now, lists: map[string]dvList{}}
}

// invalidate drops the cached status and setup (after install, uninstall or a
// change of the config).
func (d *dvSnapper) invalidate() {
	d.mu.Lock()
	d.status, d.setup, d.node, d.builtAt = dvCache[helper.DVSwitchStatus]{}, dvCache[helper.DVSwitchSetup]{}, dvCache[DVBridgeNode]{}, time.Time{}
	d.mu.Unlock()
}

func (d *dvSnapper) wired() bool {
	return d.s.d.DVSwitch != nil && (d.s.d.DVInstalled == nil || d.s.d.DVInstalled())
}

// snapshot reads ABInfo and builds a snapshot. A result younger than 400 ms
// is reused (a page load and the poll should not both hit the helper).
func (d *dvSnapper) snapshot(ctx context.Context, fresh bool) DVSnapshot {
	var info helper.DVSwitchInfo
	var err error
	if d.wired() {
		d.mu.Lock()
		if !fresh && d.have && d.now().Sub(d.builtAt) < dvCoalesceTTL {
			sn := d.last
			d.mu.Unlock()
			return sn
		}
		d.mu.Unlock()
		info, err = d.s.d.DVSwitch.DVSwitchInfo(ctx)
	}
	return d.build(ctx, info, err)
}

// build makes a snapshot from the ABInfo that was read (or its error) and
// remembers it as the latest.
func (d *dvSnapper) build(ctx context.Context, info helper.DVSwitchInfo, infoErr error) DVSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	sn := d.buildLocked(ctx, info, infoErr)
	d.last, d.have, d.builtAt = sn, true, d.now()
	return sn
}

func (d *dvSnapper) buildLocked(ctx context.Context, info helper.DVSwitchInfo, infoErr error) DVSnapshot {
	sn := DVSnapshot{Bridge: DVBridge{Source: "none"}, Modes: []DVModeView{}, Services: []DVServiceView{}, Heard: []helper.DVCaller{}, Link: DVLink{Mode: dvswitch.LinkOnDemand}}
	if !d.wired() {
		return sn
	}
	st, ok := d.getStatus(ctx)
	if !ok || !st.Installed {
		return sn
	}
	sn.Installed = true
	var enabled helper.DVModesView
	bm := false
	if v, ok := d.getSetup(ctx); ok {
		enabled = v.Modes
		bm = v.BrandMeister.Configured
	}
	for _, m := range helper.DVTuneModes {
		on := false
		switch m {
		case helper.DVModeBM:
			on = bm
		case "DMR":
			on = enabled.DMR
		case "YSF":
			on = enabled.YSF
		case "P25":
			on = enabled.P25
		case "NXDN":
			on = enabled.NXDN
		}
		sn.Modes = append(sn.Modes, DVModeView{ID: m, Enabled: on, Unlink: helper.DVUnlinkTune(m)})
	}
	gw := map[string]bool{"ysfgateway": enabled.YSF, "p25gateway": enabled.P25, "nxdngateway": enabled.NXDN}
	for _, u := range st.Units {
		switch u.Name {
		case "analog_bridge", "mmdvm_bridge":
		case "stfu":
			if !bm {
				continue
			}
		case "ysfgateway", "p25gateway", "nxdngateway":
			if !gw[u.Name] {
				continue
			}
		default:
			continue
		}
		if !u.Loaded {
			continue
		}
		active := u.Active == "active"
		sn.Services = append(sn.Services, DVServiceView{Unit: u.Name, Active: active})
		if u.Name == "analog_bridge" {
			sn.Running = active
		}
	}
	sn.Configured = sn.Installed && sn.Running
	if infoErr == nil && info.Found {
		d.fillBridge(ctx, &sn, info)
	}
	sn.BridgeNode = d.getBridgeNode(ctx)
	if sn.BridgeNode.Present {
		// read on every build (not cached with the node): a rename shows at once
		sn.BridgeNode.Name = d.s.dvNodeName()
	}
	sn.Link = d.s.dvLinkView(sn.BridgeNode)
	return sn
}

func (d *dvSnapper) fillBridge(ctx context.Context, sn *DVSnapshot, info helper.DVSwitchInfo) {
	b := &sn.Bridge
	b.Source, b.Updated = "abinfo", info.Updated
	b.Mode = helper.DVPanelMode(info)
	b.Slot, _ = strconv.Atoi(info.Slot)
	b.Muted = info.Mute == "ON"
	// ABInfo and the log show the Parrot tune as 9990 without the #.
	b.Private = (info.Private || info.TG == helper.DVParrotID) && helper.DVDMRFamily(b.Mode)
	if c := info.Callers; c != nil {
		sn.Caller = c.Current
		if c.Recent != nil {
			sn.Heard = c.Recent
		}
	}
	// last_tune survives a mode change: a value first seen in another mode is
	// not this mode's target (same rule as the tracker).
	if !d.tuneSeen || d.tuneVal != info.Tune {
		d.tuneSeen, d.tuneVal, d.tuneMode = true, info.Tune, b.Mode
	}
	switch b.Mode {
	case "":
	case helper.DVModeBM, helper.DVModeDMR:
		// TG may still hold another mode's value (a YSF reflector's number
		// after a switch that sent no tune): it is a talkgroup only when no
		// last tune is set or the last tune was set in BM or DMR.
		if info.Tune == "" || helper.DVDMRFamily(d.tuneMode) {
			b.Tune = info.TG
		}
	default:
		if d.tuneMode == b.Mode && helper.DVValidTune(b.Mode, info.Tune) {
			b.Tune = info.Tune
		}
	}
	if b.Tune != "" {
		b.TargetName = d.targetName(ctx, b.Mode, b.Tune)
	}
}

func (d *dvSnapper) getStatus(ctx context.Context) (helper.DVSwitchStatus, bool) {
	now := d.now()
	if d.status.fresh(now, dvStatusTTL) {
		return d.status.v, d.status.ok
	}
	v, err := d.s.d.DVSwitch.DVSwitchStatus(ctx)
	if err != nil {
		if d.status.ok { // keep the last good one for a while
			d.status.at = now.Add(dvStatusTTL - 5*time.Second)
			return d.status.v, true
		}
		d.status = dvCache[helper.DVSwitchStatus]{at: now.Add(dvStatusTTL - 5*time.Second)}
		return v, false
	}
	d.status = dvCache[helper.DVSwitchStatus]{v: v, ok: true, at: now}
	return v, true
}

func (d *dvSnapper) getSetup(ctx context.Context) (helper.DVSwitchSetup, bool) {
	now := d.now()
	if d.setup.fresh(now, dvStatusTTL) {
		return d.setup.v, d.setup.ok
	}
	v, err := d.s.d.DVSwitch.DVSwitchSetup(ctx)
	if err != nil {
		if d.setup.ok {
			d.setup.at = now.Add(dvStatusTTL - 5*time.Second)
			return d.setup.v, true
		}
		d.setup = dvCache[helper.DVSwitchSetup]{at: now.Add(dvStatusTTL - 5*time.Second)}
		return v, false
	}
	d.setup = dvCache[helper.DVSwitchSetup]{v: v, ok: true, at: now}
	return v, true
}

func (d *dvSnapper) getBridgeNode(ctx context.Context) DVBridgeNode {
	f := d.s.d.DVBridgeNode
	if f == nil {
		return DVBridgeNode{}
	}
	now := d.now()
	if d.node.fresh(now, dvNodeTTL) {
		return d.node.v
	}
	n, ok := f(ctx)
	v := DVBridgeNode{Present: ok}
	if ok {
		v.Node = n
	}
	d.node = dvCache[DVBridgeNode]{v: v, ok: true, at: now}
	return v
}

// targetName resolves the name of a target from the host list of the mode
// (cached) or, failing that, from a favourite. "" when unknown.
func (d *dvSnapper) targetName(ctx context.Context, mode, tune string) string {
	if n := d.listName(ctx, mode, tune); n != "" {
		return n
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

func (d *dvSnapper) listName(ctx context.Context, mode, tune string) string {
	if helper.DVDMRFamily(mode) { // the DMR list holds masters, not talkgroups
		return ""
	}
	now := d.now()
	l, ok := d.lists[mode]
	if !ok || now.Sub(l.at) >= l.ttl {
		l = dvList{names: map[string]string{}, at: now, ttl: dvListErrTTL}
		if list, err := d.s.d.DVSwitch.DVSwitchLists(ctx, strings.ToLower(mode)); err == nil {
			l.ttl = dvListTTL
			for _, it := range list.Items {
				l.names[it.Value] = it.Name
				if mode == "YSF" {
					l.names[it.Address+":"+strconv.Itoa(it.Port)] = it.Name
				}
			}
		}
		d.lists[mode] = l
	}
	return l.names[tune]
}

// publish broadcasts sn when it differs from the last published one. The
// update time is left out of the comparison: Analog_Bridge rewrites ABInfo
// often and that alone is no change.
func (d *dvSnapper) publish(sn DVSnapshot) {
	cmp := sn
	cmp.Bridge.Updated = ""
	b, err := json.Marshal(cmp)
	if err != nil {
		return
	}
	d.mu.Lock()
	changed := !d.published() || d.key != string(b)
	d.key = string(b)
	d.mu.Unlock()
	if changed && d.s.hub.count() > 0 {
		f := frame("dvswitch", sn)
		d.s.hub.broadcast(sseMsg{priv: f, pub: f})
	}
}

// published reports whether key holds a published snapshot (callers hold mu).
func (d *dvSnapper) published() bool { return d.key != "" }

// onPoll is the tracker's hook: build a snapshot from what the poll read and
// publish it if it changed.
func (d *dvSnapper) onPoll(info helper.DVSwitchInfo, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d.publish(d.build(ctx, info, err))
}

// refresh re-reads and publishes (after an own command).
func (d *dvSnapper) refresh() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	d.publish(d.snapshot(ctx, true))
}

// afterCommand publishes now and re-reads at 300 ms and 1.5 s: Analog_Bridge
// rewrites ABInfo a moment after a command.
func (d *dvSnapper) afterCommand() {
	for _, wait := range []time.Duration{300 * time.Millisecond, 1500 * time.Millisecond} {
		time.AfterFunc(wait, func() {
			select {
			case <-d.s.stop:
			default:
				d.refresh()
			}
		})
	}
}

// refreshSoon re-reads in the background (a favourite changed a target name).
func (d *dvSnapper) refreshSoon() {
	if d.s.d.DVSwitch == nil {
		return
	}
	go d.refresh()
}

// initDVSwitch creates the snapshot builder and hooks it to the tracker: the
// tracker's poll publishes changes and polls faster while a client watches.
func (s *Server) initDVSwitch() {
	s.dvs = newDVSnapper(s)
	if h, ok := s.d.DVResume.(interface {
		SetOnPoll(func(helper.DVSwitchInfo, error))
		SetViewers(func() int)
	}); ok {
		h.SetOnPoll(s.dvs.onPoll)
		h.SetViewers(s.hub.count)
	}
}
