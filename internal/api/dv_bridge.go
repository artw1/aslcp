package api

import (
	"context"
	"errors"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/store"
)

// The bridge node and the engine's target (docs/DV.md, "When the bridge node
// is unlinked"). The digital modes engine reaches the radio through the
// bridge node, so a talkgroup or reflector left tuned (and a BrandMeister
// login) after the node was unlinked only keeps a network connection open for
// nothing. ASLCP acts on its own link commands only, never on what it sees:
//
//   - When ASLCP drops the link of the bridge node (the node Unlink, a
//     schedule, an exclusive connect, disconnect all, a settings change of the
//     permanent link) and no other local node is still linked to it, the
//     engine's target is released the way a mode switch leaves a network
//     (unlink, and the on-demand session ends unless "Stay logged in" is on).
//     The mode and the remembered tune of the mode stay.
//   - When ASLCP links the bridge node to a local node and the engine has a
//     mode but no target, the remembered tune is sent again.
//
// Unlinks and links made outside ASLCP (DTMF, another node, Allmon) leave the
// target alone. Pressing Unlink on the DV card erases the remembered tune, so
// nothing comes back.

// Actors of audit entries that no user made.
var (
	dvSystemUser   = store.User{Username: "system"}
	dvScheduleUser = store.User{Username: "schedule"}
)

// dvDefaultRetuneDelay is how long the re-tune waits after a link command, so
// a tune the same request sends itself (a DV pin) lands first.
const dvDefaultRetuneDelay = time.Second

// SetDVRetuneDelay sets the wait before the re-tune after a link command;
// tests use zero.
func (s *Server) SetDVRetuneDelay(d time.Duration) { s.dvRetuneDelay = d }

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

// dvBridgeLinkedElsewhere reports whether some local node is still linked to
// the bridge node after the link node->target was dropped. An unknown state
// counts as not linked.
func (s *Server) dvBridgeLinkedElsewhere(bridge, node, target string) bool {
	m := s.monitor()
	if m == nil {
		return false
	}
	return bridgeLinkedElsewhere(m.Nodes(), func(n string) bool {
		linked, _ := (dvLinker{s}).Linked(n, bridge)
		return linked
	}, bridge, node, target)
}

// bridgeLinkedElsewhere is the rule of dvBridgeLinkedElsewhere over the local
// nodes: a target of "" is all the links of node, and a link the monitor still
// shows for the dropped pair does not count (its status may be one poll
// behind).
func bridgeLinkedElsewhere(locals []string, linked func(string) bool, bridge, node, target string) bool {
	for _, n := range locals {
		if n == bridge {
			continue
		}
		if (node == n && (target == bridge || target == "")) || (node == bridge && (target == n || target == "")) {
			continue
		}
		if linked(n) {
			return true
		}
	}
	return false
}

// dvLinkDropped is called after ASLCP dropped the link node->target ("" is all
// the links of node). When it was a link of the bridge node, the engine is on
// and a target is tuned, and no other local node is linked to the bridge node,
// the target is released. It never fails the caller: a problem is logged.
func (s *Server) dvLinkDropped(ctx context.Context, actor store.User, node, target string) {
	if s.d.DVCtl == nil || s.dvs == nil || !s.dvEnabled() {
		return
	}
	if _, _, tuned := s.dvTunedNow(); !tuned {
		return
	}
	bridge := s.dvBridgeNumber(ctx)
	if bridge == "" || (node != bridge && target != bridge) {
		return
	}
	if s.dvBridgeLinkedElsewhere(bridge, node, target) {
		return
	}
	s.dvBridgeRelease(ctx, actor)
}

// dvLinkMade is called after ASLCP linked node to target. When one side is the
// bridge node, the remembered tune is sent again, after a short wait and off
// the request, if the engine has a mode and no target.
func (s *Server) dvLinkMade(actor store.User, node, target string) {
	if s.d.DVCtl == nil || s.dvs == nil || !s.dvEnabled() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		bridge := s.dvBridgeNumber(ctx)
		if bridge == "" || (node != bridge && target != bridge) {
			return
		}
		if d := s.dvRetuneDelay; d > 0 {
			time.Sleep(d)
		}
		s.dvBridgeRestore(ctx, actor)
	}()
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
func (s *Server) dvBridgeRelease(ctx context.Context, actor store.User) {
	if s.d.DVCtl == nil || !s.dvEnabled() {
		return
	}
	s.dvBridgeMu.Lock()
	defer s.dvBridgeMu.Unlock()
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
	s.audit(actor, "dv.bridge.released", map[string]any{"mode": mode, "target": tune})
	s.d.Logger.Printf("dv bridge: the bridge node was unlinked by %s, released %s %s", actor.Username, mode, tune)
	s.dvs.refreshSoon()
}

// dvBridgeRestore sends the remembered tune again after the bridge node was
// linked, when the engine has a mode and no target and no tune is in flight
// (a DV pin tunes its own target).
func (s *Server) dvBridgeRestore(ctx context.Context, actor store.User) {
	if s.d.DVCtl == nil || !s.dvEnabled() {
		return
	}
	s.dvBridgeMu.Lock()
	defer s.dvBridgeMu.Unlock()
	if s.dvTuning.Load() > 0 {
		return
	}
	mode, _, tuned := s.dvTunedNow()
	if mode == "" || tuned {
		return
	}
	tune := s.dvRememberedTune(mode)
	if tune == "" {
		return
	}
	st, _, err := s.dvSendTune(ctx, mode, tune)
	if err != nil {
		s.d.Logger.Printf("dv bridge: cannot tune %s %s again: %v", mode, tune, err)
		return
	}
	s.dvs.setStatus(st)
	s.audit(actor, "dv.bridge.retuned", map[string]any{"mode": mode, "tune": tune})
	s.d.Logger.Printf("dv bridge: the bridge node was linked by %s, tuned %s %s again", actor.Username, mode, tune)
	s.dvs.refreshSoon()
}
