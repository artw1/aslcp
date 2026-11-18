package dvswitch

import (
	"context"
	"strings"
	"sync"
	"time"
)

// The permanent link reconciler (docs/DVSWITCH.md, "Link mode"): in
// "permanent" mode the bridge node stays linked to the chosen local node, so
// the reconciler re-issues `ilink 13` (permanent transceive) from the local
// node when the link is missing. It is level-triggered: it checks at ASLCP
// start, whenever it is kicked (an Asterisk restart, a saved setup) and on a
// slow tick, which also catches an Asterisk restart done outside the panel.
//
// A manual disconnect of the link in the panel pauses it (dvswitch.link_paused)
// so that the operator's decision sticks. Saving the setup or restarting
// Asterisk through the panel clears the pause. The reconciler's own commands
// never count as manual: they go through the Linker, not the HTTP handlers.

// Settings keys (the settings table).
const (
	SettingNode       = "dvswitch.node"        // the bridge node number
	SettingLinkMode   = "dvswitch.link_mode"   // "ondemand" (default) or "permanent"
	SettingLinkNode   = "dvswitch.link_node"   // the local node the bridge links to
	SettingLinkPaused = "dvswitch.link_paused" // "1" after a manual disconnect
	// SettingLinkExclusive is "Disconnect other links first": "1" or "0";
	// absent means DefaultLinkExclusive. It needs no rpt.conf change.
	SettingLinkExclusive = "dvswitch.link_exclusive"
)

// DefaultLinkExclusive is the default of "Disconnect other links first": on,
// like the analog Connect's "Disconnect before Connect" checkbox.
const DefaultLinkExclusive = true

// ExclusiveWindow is how long a save's "disconnect others first" stays armed
// for the reconciler's next connect (it is dropped when the link is up).
const ExclusiveWindow = 5 * time.Minute

// Link modes.
const (
	LinkOnDemand  = "ondemand"
	LinkPermanent = "permanent"
)

// LinkStore is the settings storage of the reconciler.
type LinkStore interface {
	GetSetting(k string) (string, error)
	SetSetting(k, v string) error
}

// Linker sends the link commands and reports the link state.
type Linker interface {
	// Connect issues a permanent transceive link from local to bridge. With
	// exclusive it first disconnects the other links of local (the analog
	// "disconnect before connect" path).
	Connect(ctx context.Context, local, bridge string, exclusive bool) error
	// Disconnect removes the link; permanent also removes it from the
	// node's rc.conf-style reconnect list (ilink 11).
	Disconnect(ctx context.Context, local, bridge string, permanent bool) error
	// Linked reports whether local is linked to (or connecting to) bridge.
	// known is false when the monitor has no status of local (Asterisk down,
	// not a local node, not polled yet).
	Linked(local, bridge string) (linked, known bool)
}

// LinkSettings is the link configuration saved by the setup.
type LinkSettings struct {
	Mode      string `json:"mode"`
	LocalNode string `json:"localNode"`
	Bridge    string `json:"bridge"`
	// Exclusive is "Disconnect other links first" (SettingLinkExclusive).
	Exclusive bool `json:"exclusive"`
}

// ReadLinkSettings reads the settings; the mode defaults to ondemand.
func ReadLinkSettings(st LinkStore) LinkSettings {
	get := func(k string) string {
		v, _ := st.GetSetting(k)
		return strings.TrimSpace(v)
	}
	l := LinkSettings{Mode: get(SettingLinkMode), LocalNode: get(SettingLinkNode), Bridge: get(SettingNode)}
	if l.Mode != LinkPermanent {
		l.Mode = LinkOnDemand
	}
	switch get(SettingLinkExclusive) {
	case "1":
		l.Exclusive = true
	case "0":
	default:
		l.Exclusive = DefaultLinkExclusive
	}
	return l
}

// WriteLinkSettings saves the settings.
func WriteLinkSettings(st LinkStore, l LinkSettings) error {
	excl := "0"
	if l.Exclusive {
		excl = "1"
	}
	for _, kv := range [][2]string{{SettingNode, l.Bridge}, {SettingLinkMode, l.Mode}, {SettingLinkNode, l.LocalNode}, {SettingLinkExclusive, excl}} {
		if err := st.SetSetting(kv[0], kv[1]); err != nil {
			return err
		}
	}
	return nil
}

// LinkReconciler keeps the permanent link. Create it with a struct literal
// (Store and Linker are required) and call Run.
type LinkReconciler struct {
	Store  LinkStore
	Linker Linker
	Now    func() time.Time
	Logger interface{ Printf(string, ...any) }
	// OnChange is called (without locks held) when the state may have
	// changed: a pause set or cleared, a link command sent.
	OnChange func()
	// StartDelay waits for the monitor to know the nodes before the first
	// check; Tick is the period of the level check (default 60 s); Retry is
	// the minimum time between two connect attempts (default 30 s);
	// CmdTimeout bounds one command (default 20 s).
	StartDelay, Tick, Retry, CmdTimeout time.Duration

	once sync.Once
	wake chan struct{}
	mu   sync.Mutex // guards last and exclUntil
	run  sync.Mutex // serialises RunOnce
	last time.Time
	// exclUntil: until then the next connect is exclusive (armed by Saved
	// when the operator saved the setup with "disconnect others first").
	exclUntil time.Time
}

func (r *LinkReconciler) init() {
	r.once.Do(func() { r.wake = make(chan struct{}, 1) })
}

func (r *LinkReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *LinkReconciler) logf(f string, a ...any) {
	if r.Logger != nil {
		r.Logger.Printf(f, a...)
	}
}

func (r *LinkReconciler) notify() {
	if r.OnChange != nil {
		r.OnChange()
	}
}

// Kick asks Run for an immediate check.
func (r *LinkReconciler) Kick() {
	r.init()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Paused reports the manual-disconnect pause.
func (r *LinkReconciler) Paused() bool {
	v, _ := r.Store.GetSetting(SettingLinkPaused)
	return v == "1"
}

func (r *LinkReconciler) setPaused(p bool) {
	if r.Paused() == p {
		return
	}
	v := ""
	if p {
		v = "1"
	}
	if err := r.Store.SetSetting(SettingLinkPaused, v); err != nil {
		r.logf("dvswitch link: cannot save the pause: %v", err)
		return
	}
	r.notify()
}

// ManualDisconnect is called when the operator disconnected node from target
// in the panel (never for the reconciler's own commands). In permanent mode,
// disconnecting the bridge node from the chosen local node pauses the
// reconciler; any other disconnect is none of its business. It reports
// whether it paused.
func (r *LinkReconciler) ManualDisconnect(node, target string) bool {
	l := ReadLinkSettings(r.Store)
	if l.Mode != LinkPermanent || l.Bridge == "" || node != l.LocalNode || (target != "" && target != l.Bridge) {
		return false
	}
	r.setPaused(true)
	return true
}

// ManualConnect is called when the operator linked node to target in the
// panel: linking the bridge node again ends the pause.
func (r *LinkReconciler) ManualConnect(node, target string) {
	l := ReadLinkSettings(r.Store)
	if l.Mode == LinkPermanent && l.Bridge != "" && node == l.LocalNode && target == l.Bridge {
		r.setPaused(false)
	}
}

// AsteriskRestarted clears the pause and checks the link again. This relink
// is automatic, so it is never exclusive (and drops an armed exclusive).
func (r *LinkReconciler) AsteriskRestarted() {
	r.setPaused(false)
	r.mu.Lock()
	r.exclUntil = time.Time{}
	r.last = time.Time{}
	r.mu.Unlock()
	r.Kick()
}

// Saved is called after the setup saved new settings: it removes a permanent
// link that no longer applies (the mode went back to ondemand, or the local or
// bridge node changed), clears the pause and checks the link again. The
// removal is the reconciler's own command, not a manual disconnect.
func (r *LinkReconciler) Saved(ctx context.Context, prev, cur LinkSettings) {
	if prev.Mode == LinkPermanent && prev.LocalNode != "" && prev.Bridge != "" &&
		(cur.Mode != LinkPermanent || cur.LocalNode != prev.LocalNode || cur.Bridge != prev.Bridge) {
		if linked, known := r.Linker.Linked(prev.LocalNode, prev.Bridge); !known || linked {
			if err := r.cmd(ctx, func(c context.Context) error {
				return r.Linker.Disconnect(c, prev.LocalNode, prev.Bridge, true)
			}); err != nil {
				r.logf("dvswitch link: cannot remove the old permanent link: %v", err)
			}
		}
	}
	r.setPaused(false)
	r.mu.Lock()
	r.last = time.Time{}
	// EXCLUSIVITY RULE: only an operator action in the panel may disconnect
	// other links. A save is one, so it arms "disconnect others first" for
	// the connect it triggers. The reconciler's own relinks (start, tick,
	// Asterisk restart) never arm it, so they cannot fight links made by hand.
	r.exclUntil = time.Time{}
	if cur.Exclusive && cur.Mode == LinkPermanent {
		r.exclUntil = r.now().Add(ExclusiveWindow)
	}
	r.mu.Unlock()
	r.Kick()
}

// Unlink removes the permanent link before an uninstall (own command). It is
// a no-op unless the link is permanent.
func (r *LinkReconciler) Unlink(ctx context.Context) error {
	l := ReadLinkSettings(r.Store)
	if l.Mode != LinkPermanent || l.LocalNode == "" || l.Bridge == "" {
		return nil
	}
	return r.cmd(ctx, func(c context.Context) error { return r.Linker.Disconnect(c, l.LocalNode, l.Bridge, true) })
}

func (r *LinkReconciler) cmd(ctx context.Context, f func(context.Context) error) error {
	to := r.CmdTimeout
	if to <= 0 {
		to = 20 * time.Second
	}
	c, cancel := context.WithTimeout(ctx, to)
	defer cancel()
	err := f(c)
	r.notify()
	return err
}

// Run checks at start (after StartDelay), when kicked and on every tick.
func (r *LinkReconciler) Run(ctx context.Context) {
	r.init()
	if r.StartDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(r.StartDelay):
		}
	}
	tick := r.Tick
	if tick <= 0 {
		tick = 60 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		r.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-r.wake:
		}
	}
}

// RunOnce does one check. Exported for tests.
func (r *LinkReconciler) RunOnce(ctx context.Context) {
	r.init()
	r.run.Lock()
	defer r.run.Unlock()
	l := ReadLinkSettings(r.Store)
	if l.Mode != LinkPermanent || l.LocalNode == "" || l.Bridge == "" || r.Paused() {
		return
	}
	linked, known := r.Linker.Linked(l.LocalNode, l.Bridge)
	if linked {
		r.mu.Lock()
		r.exclUntil = time.Time{} // nothing to connect: the armed exclusive is spent
		r.mu.Unlock()
		return
	}
	if !known {
		return
	}
	retry := r.Retry
	if retry <= 0 {
		retry = 30 * time.Second
	}
	r.mu.Lock()
	if !r.last.IsZero() && r.now().Sub(r.last) < retry {
		r.mu.Unlock()
		return
	}
	r.last = r.now()
	// exclusive only when armed by a save in the panel and still fresh
	excl := !r.exclUntil.IsZero() && r.now().Before(r.exclUntil)
	r.mu.Unlock()
	r.logf("dvswitch link: node %s is not linked to the bridge node %s, linking it (permanent, exclusive=%v)", l.LocalNode, l.Bridge, excl)
	err := r.cmd(ctx, func(c context.Context) error { return r.Linker.Connect(c, l.LocalNode, l.Bridge, excl) })
	if err == nil && excl {
		r.mu.Lock()
		r.exclUntil = time.Time{}
		r.mu.Unlock()
	}
	if err != nil {
		r.logf("dvswitch link: cannot link %s to %s: %v", l.LocalNode, l.Bridge, err)
	}
}
