package api

import (
	"context"
	"errors"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/store"
)

// The bridge node watcher (docs/DV.md, "When the bridge node is unlinked").
// The digital modes engine reaches the radio through the bridge node, so
// when no local node is linked to it any more, a talkgroup or reflector left
// tuned (and a BrandMeister login) would only keep a network connection open
// for nothing. This works server side, so it does not matter who unlinked:
// the tile, the DV card, a schedule, DTMF or a remote node.
//
//   - A linked to unlinked change that lasts dvBridgeGrace releases the
//     engine's target the way a mode switch leaves a network (unlink, and the
//     on-demand session ends unless "Stay logged in" is on). The mode and the
//     remembered tune of the mode stay.
//   - When the bridge node is linked again and the engine has no target in its
//     mode, the remembered tune is sent again.
//
// Pressing Unlink on the DV card is another thing: it erases the remembered
// tune, so nothing comes back.

// dvBridgeGrace is how long the bridge node stays unlinked before the target
// is released: an Asterisk restart that brings a permanent link back at once
// does nothing.
const dvBridgeGrace = 5 * time.Second

// dvBridgeTick is how often the link is read.
const dvBridgeTick = time.Second

// dvSystemUser is the actor of audit entries of automatic actions.
var dvSystemUser = store.User{Username: "system"}

// dvBridgeWatch is the state machine of the watcher. It runs on one
// goroutine; step is the whole logic, with the clock and the link read passed
// in so tests drive it.
type dvBridgeWatch struct {
	s     *Server
	grace time.Duration
	// act hooks, replaced by tests that only look at the decisions
	release func(context.Context)
	restore func(context.Context) (done bool)

	linked    bool      // the last known state
	seen      bool      // a known state was read
	lostAt    time.Time // when the loss was first seen; zero while linked
	lost      bool      // a loss was seen and the link is not back yet
	released  bool      // the release of this loss was handled
	restoring bool      // linked again; the re-tune is still due
}

func newDVBridgeWatch(s *Server) *dvBridgeWatch {
	w := &dvBridgeWatch{s: s, grace: dvBridgeGrace}
	w.release, w.restore = s.dvBridgeRelease, s.dvBridgeRestore
	return w
}

// step takes one reading of the link: linked is true when some local node is
// linked to the bridge node, known is false while the monitor cannot say
// (Asterisk down, not polled yet), which changes nothing.
func (w *dvBridgeWatch) step(ctx context.Context, now time.Time, linked, known bool) {
	if !known {
		return
	}
	switch {
	case linked:
		if w.seen && !w.linked && w.lost {
			w.restoring = true // back after a loss
		}
		w.linked, w.seen = true, true
		w.lostAt, w.lost, w.released = time.Time{}, false, false
		if w.restoring && w.restore(ctx) {
			w.restoring = false
		}
	case w.seen && w.linked:
		// linked to unlinked: only a change seen while running counts
		w.linked, w.lostAt, w.lost, w.released = false, now, true, false
		w.restoring = false
		w.maybeRelease(ctx, now)
	default:
		w.linked, w.seen = false, true
		if w.lost && !w.released {
			w.maybeRelease(ctx, now)
		}
	}
}

func (w *dvBridgeWatch) maybeRelease(ctx context.Context, now time.Time) {
	if now.Sub(w.lostAt) < w.grace {
		return
	}
	w.released = true
	w.release(ctx)
}

// dvBridgeLoop reads the link every dvBridgeTick until ctx ends.
func (s *Server) dvBridgeLoop(ctx context.Context) {
	w := newDVBridgeWatch(s)
	t := time.NewTicker(dvBridgeTick)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			linked, known := s.dvBridgeLinked(ctx)
			w.step(ctx, now, linked, known)
		}
	}
}

// dvBridgeNumber is the bridge node number: the saved one, else the node the
// detection found.
func (s *Server) dvBridgeNumber(ctx context.Context) string {
	if b := s.dvLinkSettings().Bridge; b != "" {
		return b
	}
	if s.d.DVBridgeNode != nil {
		if n, ok := s.d.DVBridgeNode(ctx); ok {
			return n
		}
	}
	return ""
}

// dvBridgeLinked reports whether some local node is linked to the bridge
// node. known is false when there is no bridge node, no monitor or no local
// node has a status yet.
func (s *Server) dvBridgeLinked(ctx context.Context) (linked, known bool) {
	if !s.dvEnabled() {
		return false, false
	}
	bridge := s.dvBridgeNumber(ctx)
	m := s.monitor()
	if bridge == "" || m == nil {
		return false, false
	}
	for _, n := range m.Nodes() {
		if n == bridge {
			continue
		}
		l, k := (dvLinker{s}).Linked(n, bridge)
		if l {
			return true, true
		}
		known = known || k
	}
	return false, known
}

// dvTunedNow is the mode and tune the engine shows, and whether a target is
// set. The echo test is no target.
func (s *Server) dvTunedNow() (mode, tune string, tuned bool) {
	in := s.dvs.info()
	if in.Mode == "" || in.Mode == dvpanel.ModeLoop || in.Tune == "" || dvpanel.UnlinkValue(in.Mode, in.Tune) {
		return in.Mode, "", false
	}
	return in.Mode, in.Tune, true
}

// dvBridgeRelease drops the target of the engine after the bridge node was
// unlinked. The remembered tune is kept (and filled in when the tracker has
// not seen this tune yet), so it comes back with the link.
func (s *Server) dvBridgeRelease(ctx context.Context) {
	if s.d.DVCtl == nil || !s.dvEnabled() {
		return
	}
	mode, tune, tuned := s.dvTunedNow()
	if !tuned {
		return
	}
	if s.dvRememberedTune(mode) == "" && dvpanel.ValidTune(mode, tune) && !dvpanel.PrivateTune(mode, tune) {
		s.dvRememberTune(mode, tune)
	}
	st, err := s.d.DVCtl.Unlink(ctx, ctl.UnlinkArgs{Mode: mode, Release: true})
	var ce *ctl.Error
	if errors.As(err, &ce) && ce.Code == ctl.CodeBadRequest {
		// an engine older than Release: it still unlinks, only the session
		// of an on-demand network stays
		st, err = s.d.DVCtl.Unlink(ctx, ctl.UnlinkArgs{Mode: mode})
	}
	if err != nil {
		s.d.Logger.Printf("dv bridge: cannot release %s after the bridge node was unlinked: %v", mode, err)
		return
	}
	s.dvs.setStatus(st)
	s.audit(dvSystemUser, "dv.bridge.released", map[string]any{"mode": mode, "target": tune})
	s.d.Logger.Printf("dv bridge: no local node is linked to the bridge node, released %s %s", mode, tune)
	s.dvs.refreshSoon()
}

// dvBridgeRestore sends the remembered tune again after the bridge node was
// linked. done is false only when a tune is in flight and the check should be
// repeated.
func (s *Server) dvBridgeRestore(ctx context.Context) (done bool) {
	if s.d.DVCtl == nil || !s.dvEnabled() {
		return true
	}
	if s.dvTuning.Load() > 0 {
		return false
	}
	mode, _, tuned := s.dvTunedNow()
	if mode == "" || tuned {
		return true
	}
	tune := s.dvRememberedTune(mode)
	if tune == "" {
		return true
	}
	st, _, err := s.dvSendTune(ctx, mode, tune)
	if err != nil {
		s.d.Logger.Printf("dv bridge: cannot tune %s %s again: %v", mode, tune, err)
		return true
	}
	s.dvs.setStatus(st)
	s.audit(dvSystemUser, "dv.bridge.retuned", map[string]any{"mode": mode, "tune": tune})
	s.d.Logger.Printf("dv bridge: the bridge node is linked again, tuned %s %s", mode, tune)
	s.dvs.refreshSoon()
	return true
}
