package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// Taking over a bridge node (docs/DV.md, "Take over a bridge node"): a
// node that looks exactly like the one ASLCP adds, but has no record (a backup
// of 1.2.3 or earlier restored onto a new node, or a node made by hand), gets
// a record built from what is in the files now. From then on ASLCP manages it
// like its own: Change number, Remove bridge node and the uninstall revert it.
// Nothing in rpt.conf, or modules.conf is written.

func (s *Server) dvAdoptRoutes() {
	s.mux.HandleFunc("POST /api/dv/private-node/adopt", s.authed(store.RoleAdmin, s.handleDVAdopt))
}

func notAdoptable(f string, a ...any) error {
	return perr(http.StatusConflict, "not_adoptable", f, a...)
}

// dvAdoptRecord checks that the bridge node in rpt.conf is the one ASLCP would
// have added and returns the record that makes a later removal restore the
// stock state. The error is a *dvPrivErr with status 409 for "no" and the
// usual errors for files that cannot be read.
//
// The record values (what the removal then does):
//   - StanzaAdded and StanzaText: the stanza as it stands, so the removal
//     deletes it (only while it still reads like this), NodesLineAdded and
//     NodesLine the same for the [nodes] line;
//   - ChanUsrpWas "noload" when modules.conf has a load line (the removal turns
//     it back into the noload line of the ASL3 stock file), "load" when
//     autoload or a missing modules.conf loads it (nothing to revert);
//   - UsrpUntouched always: the engine takes its ports from the stanza, there
//     is nothing to restore.
func (s *Server) dvAdoptRecord(ctx context.Context, setup dvSetup) (*helper.DVPrivateRecord, error) {
	if setup.Private != nil {
		return nil, perr(http.StatusConflict, "already_managed", "ASLCP already manages the bridge node %s", setup.Private.Node)
	}
	svc := s.d.ConfigEditor
	if svc == nil {
		return nil, perr(http.StatusServiceUnavailable, "config_unavailable", "configuration editing is not available")
	}
	rptC, err := svc.Read(ctx, dvRptFile)
	if err != nil {
		return nil, err
	}
	tree, err := svc.Tree(ctx, dvRptFile)
	if err != nil {
		return nil, err
	}
	chans := dvpanel.USRPChannels(tree)
	link := s.dvLinkSettings()
	var ch *dvpanel.USRPChannel
	switch {
	case link.Bridge != "":
		for i := range chans {
			if chans[i].Node == link.Bridge {
				ch = &chans[i]
			}
		}
		if ch == nil {
			return nil, notAdoptable("node %s is the bridge node in ASLCP's settings, but it has no USRP channel in rpt.conf", link.Bridge)
		}
	case len(chans) == 0:
		return nil, perr(http.StatusConflict, "no_bridge_node", "there is no bridge node in rpt.conf to take over")
	case len(chans) > 1:
		return nil, notAdoptable("rpt.conf has %d nodes with a USRP channel and ASLCP cannot tell which one is the bridge node", len(chans))
	default:
		ch = &chans[0]
	}
	node := ch.Node
	if _, err := dvpanel.ValidNode(node); err != nil {
		return nil, notAdoptable("node %s is not a private node (below 2000)", node)
	}
	if !ch.Standard {
		return nil, notAdoptable("node %s does not have a channel of the form USRP/127.0.0.1:<port>:<port>, so it is not the node ASLCP adds", node)
	}
	stanza, line, ok := astconf.ExistingBridgeNode([]byte(rptC.Content), node)
	if !ok {
		return nil, notAdoptable("the stanza [%s] is not in rpt.conf itself or is defined more than once", node)
	}
	if line == "" {
		return nil, notAdoptable("node %s has no line in the [nodes] section of rpt.conf", node)
	}
	if len(stanza) > 4096 || len(line) > 256 {
		return nil, notAdoptable("the stanza or [nodes] line of node %s is unusually large", node)
	}
	rec := &helper.DVPrivateRecord{Version: 1, Node: node, StanzaAdded: true, StanzaText: stanza, NodesLineAdded: true, NodesLine: line,
		At: time.Now().UTC().Format(time.RFC3339)}

	modC, mErr := svc.Read(ctx, dvModulesFile)
	switch {
	case mErr == nil:
		st, err := astconf.ModuleState([]byte(modC.Content), astconf.ModuleUSRP)
		if err != nil {
			return nil, perr(http.StatusUnprocessableEntity, "syntax_error", "modules.conf: %v", err)
		}
		switch st {
		case astconf.ModExplicit:
			rec.ChanUsrpWas = astconf.ModNoload
		case astconf.ModAuto:
			rec.ChanUsrpWas = astconf.ModLoad
		default:
			return nil, notAdoptable("chan_usrp.so is not loaded by modules.conf, so node %s cannot be a working bridge node", node)
		}
	default:
		var ne *configsvc.NotFoundError
		if !errors.As(mErr, &ne) {
			return nil, mErr
		}
		rec.ChanUsrpWas = astconf.ModLoad // no modules.conf: Asterisk autoloads, nothing to revert
	}

	rec.UsrpUntouched = true
	if link.Bridge == node {
		rec.LinkMode, rec.LocalNode = link.Mode, link.LocalNode
	}
	return rec, nil
}

// dvAdoptable says whether the bridge node found in rpt.conf can be taken over.
func (s *Server) dvAdoptable(ctx context.Context, setup dvSetup) bool {
	if setup.Private != nil || s.d.ConfigEditor == nil {
		return false
	}
	_, err := s.dvAdoptRecord(ctx, setup)
	return err == nil
}

// handleDVAdopt: POST on the private-node adopt route, admin. The body is
// ignored. 409 already_managed (a record exists), 409 no_bridge_node, 409
// not_adoptable (the node does not look like the one ASLCP adds; the message
// says what differs).
func (s *Server) handleDVAdopt(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct{}
	if !decode(w, r, &in, true) {
		return
	}
	if s.d.DVEngine == nil {
		s.dvPrivateError(w, errDVUnavailable)
		return
	}
	s.dvAdoptMu.Lock()
	defer s.dvAdoptMu.Unlock()
	ctx := r.Context()
	setup, err := s.dvSetupState(ctx)
	var rec *helper.DVPrivateRecord
	if err == nil {
		rec, err = s.dvAdoptRecord(ctx, setup)
	}
	if err != nil {
		s.audit(u, "dv.private.adopt.refused", map[string]any{"error": err.Error()})
		s.dvPrivateError(w, err)
		return
	}
	if _, err := s.d.DVEngine.DVPrivateSet(ctx, *rec); err != nil {
		s.dvPrivateError(w, err)
		return
	}
	s.audit(u, "dv.private.adopt", map[string]any{"node": rec.Node})
	s.d.Logger.Printf("dv bridge node %s taken over by %s", rec.Node, u.Username)
	s.dvs.invalidate()
	s.dvs.refreshSoon()
	writeJSON(w, 200, map[string]any{"adopted": true, "node": rec.Node, "chanUsrpWas": rec.ChanUsrpWas})
}
