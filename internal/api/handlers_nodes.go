package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/auth"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

var (
	reConnectTarget    = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)
	reDisconnectTarget = regexp.MustCompile(`^[A-Za-z1-9][A-Za-z0-9-]{0,19}$`)
)

func (s *Server) handleNodes(w http.ResponseWriter, r *http.Request, u store.User) {
	writeJSON(w, 200, s.allNodes(auth.RoleRank(u.Role) >= auth.RoleRank(store.RoleOperator)))
}

// localNode checks the {node} path value against the monitored nodes.
func (s *Server) localNode(w http.ResponseWriter, r *http.Request) (string, bool) {
	node := r.PathValue("node")
	if m := s.monitor(); m != nil {
		for _, n := range m.Nodes() {
			if n == node {
				return node, true
			}
		}
	}
	writeErr(w, http.StatusNotFound, "unknown_node", "no such local node")
	return "", false
}

// commandError maps an rpt/AMI error to an HTTP response.
func (s *Server) commandError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ami.ErrNotConnected), errors.Is(err, ami.ErrClosed):
		writeErr(w, http.StatusServiceUnavailable, "ami_down", "Asterisk is not connected")
	case errors.Is(err, context.DeadlineExceeded):
		writeErr(w, http.StatusGatewayTimeout, "timeout", "Asterisk did not answer in time")
	default:
		s.d.Logger.Printf("command failed: %v", err)
		writeErr(w, http.StatusBadGateway, "command_failed", err.Error())
	}
}

func (s *Server) cmdContext(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), 30*time.Second)
}

func (s *Server) afterCommand(node string) {
	if m := s.monitor(); m != nil {
		m.Refresh(node)
	}
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request, u store.User) {
	node, ok := s.localNode(w, r)
	if !ok {
		return
	}
	var in struct {
		Target    string `json:"target"`
		Mode      string `json:"mode"`
		Permanent bool   `json:"permanent"`
		Exclusive bool   `json:"exclusive"` // absent means false (API compatibility)
	}
	if !decode(w, r, &in, false) {
		return
	}
	in.Target = strings.TrimSpace(in.Target)
	if !reConnectTarget.MatchString(in.Target) || in.Target == node {
		writeErr(w, 400, "bad_target", "target must be a node number (not this node)")
		return
	}
	var mode rpt.ConnectMode
	switch in.Mode {
	case "", "transceive":
		mode = rpt.ConnTransceive
	case "monitor":
		mode = rpt.ConnMonitor
	case "localmonitor":
		mode = rpt.ConnLocalMonitor
	default:
		writeErr(w, 400, "bad_request", `mode must be "transceive", "monitor" or "localmonitor"`)
		return
	}
	ctx, cancel := s.cmdContext(r)
	defer cancel()
	// "Disconnect before connect" (AllScan's feature) is done by connectLink;
	// localmonitor never passes audio to other links, so it cannot cross-link
	// and ignores the flag.
	exclusive := in.Exclusive && exclusiveMode(mode)
	if err := s.connectLink(ctx, node, in.Target, mode, in.Permanent, exclusive); err != nil {
		s.commandError(w, err)
		return
	}
	s.d.Logger.Printf("connect %s -> %s (%s, permanent=%v, exclusive=%v) by %s", node, in.Target, mode, in.Permanent, exclusive, u.Username)
	s.afterCommand(node)
	s.manualSchedules(node, fmt.Sprintf("manual connect %s on %s by %s", in.Target, node, u.Username))
	w.WriteHeader(http.StatusAccepted)
}

// exclusiveMode reports whether "disconnect before connect" applies to mode.
func exclusiveMode(mode rpt.ConnectMode) bool {
	return mode == rpt.ConnTransceive || mode == rpt.ConnMonitor
}

// connectLink connects node to target, first dropping the other direct links
// when exclusive is set (and the mode allows it). Both the HTTP handler and
// the scheduler use it, so they share the command pacing and behaviour.
func (s *Server) connectLink(ctx context.Context, node, target string, mode rpt.ConnectMode, permanent, exclusive bool) error {
	if !s.amiUp() {
		return ami.ErrNotConnected
	}
	if exclusive && exclusiveMode(mode) {
		if err := s.disconnectOthers(ctx, node, target); err != nil {
			return err
		}
	}
	if err := s.rpt.Connect(ctx, node, target, mode, permanent); err != nil {
		return err
	}
	s.afterCommand(node)
	return nil
}

// disconnectLink sends a (permanent or normal) disconnect of one link.
func (s *Server) disconnectLink(ctx context.Context, node, target string, permanent bool) error {
	if !s.amiUp() {
		return ami.ErrNotConnected
	}
	if err := s.rpt.Disconnect(ctx, node, target, permanent); err != nil {
		return err
	}
	s.afterCommand(node)
	return nil
}

// disconnectOthers sends a normal (non-permanent) disconnect (ilink 1) for
// every current direct link of `node` except `keep`, using the monitor's
// latest status (no fresh poll). app_rpt silently ignores a non-permanent
// disconnect of a permanent link, so permanent links survive. The target
// itself is never disconnected, so a mode change on an existing link works.
// Links whose name the disconnect validator rejects are skipped. The first
// command error aborts, so the caller does not connect.
func (s *Server) disconnectOthers(ctx context.Context, node, keep string) error {
	m := s.monitor()
	if m == nil {
		return nil
	}
	st, ok := m.Status(node)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for _, l := range st.Links {
		if l.Node == keep || seen[l.Node] {
			continue
		}
		seen[l.Node] = true
		if !reDisconnectTarget.MatchString(l.Node) {
			s.d.Logger.Printf("debug: exclusive connect on %s: skipping link %q (not a valid disconnect target)", node, l.Node)
			continue
		}
		if err := s.rpt.Disconnect(ctx, node, l.Node, false); err != nil {
			return err
		}
		// Wait until app_rpt has actually dropped the link before the next
		// command: its single command slot is busy until then, and a
		// command sent meanwhile is refused silently.
		s.waitLinkGone(ctx, node, l.Node)
	}
	return nil
}

// waitLinkGone polls until link is no longer a direct link of node, or
// LinkGoneWait passes (a permanent link survives a normal disconnect).
func (s *Server) waitLinkGone(ctx context.Context, node, link string) {
	m := s.monitor()
	if m == nil || s.LinkGoneWait <= 0 {
		return
	}
	deadline := time.Now().Add(s.LinkGoneWait)
	for time.Now().Before(deadline) {
		m.Refresh(node)
		t := time.NewTimer(150 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C:
		}
		st, ok := m.Status(node)
		if !ok {
			return
		}
		gone := true
		for _, l := range st.Links {
			if l.Node == link {
				gone = false
				break
			}
		}
		if gone {
			return
		}
	}
	s.d.Logger.Printf("exclusive connect on %s: link %s still present after %s; connecting anyway", node, link, s.LinkGoneWait)
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request, u store.User) {
	node, ok := s.localNode(w, r)
	if !ok {
		return
	}
	var in struct {
		Target    string `json:"target"`
		Permanent bool   `json:"permanent"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	in.Target = strings.TrimSpace(in.Target)
	if !reDisconnectTarget.MatchString(in.Target) {
		writeErr(w, 400, "bad_target", "invalid target")
		return
	}
	ctx, cancel := s.cmdContext(r)
	defer cancel()
	if err := s.disconnectLink(ctx, node, in.Target, in.Permanent); err != nil {
		s.commandError(w, err)
		return
	}
	s.d.Logger.Printf("disconnect %s -> %s (permanent=%v) by %s", node, in.Target, in.Permanent, u.Username)
	s.manualSchedules(node, fmt.Sprintf("manual disconnect %s on %s by %s", in.Target, node, u.Username))
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleDisconnectAll(w http.ResponseWriter, r *http.Request, u store.User) {
	node, ok := s.localNode(w, r)
	if !ok {
		return
	}
	if !s.amiUp() {
		s.commandError(w, ami.ErrNotConnected)
		return
	}
	ctx, cancel := s.cmdContext(r)
	defer cancel()
	if err := s.rpt.DisconnectAll(ctx, node); err != nil {
		s.commandError(w, err)
		return
	}
	s.d.Logger.Printf("disconnect-all %s by %s", node, u.Username)
	s.afterCommand(node)
	s.manualSchedules(node, fmt.Sprintf("manual disconnect all on %s by %s", node, u.Username))
	w.WriteHeader(http.StatusAccepted)
}

// ------------------------------------------------------------ misc routes

func (s *Server) handleLookup(w http.ResponseWriter, r *http.Request, _ store.User) {
	node := r.PathValue("node")
	if s.d.Nodelist != nil {
		if in, ok := s.d.Nodelist.Lookup(node); ok {
			writeJSON(w, 200, in)
			return
		}
	}
	writeErr(w, 404, "not_found", "node not in the node list")
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request, _ store.User) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			writeErr(w, 400, "bad_request", "invalid limit")
			return
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}
	if s.d.Nodelist == nil {
		writeJSON(w, 200, []struct{}{})
		return
	}
	writeJSON(w, 200, s.d.Nodelist.Search(r.URL.Query().Get("q"), limit))
}

func (s *Server) handleNodelist(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.Nodelist == nil {
		writeJSON(w, 200, map[string]any{"count": 0, "updated": nil, "source": "none", "error": nil})
		return
	}
	writeJSON(w, 200, s.d.Nodelist.Status())
}

func (s *Server) handleNodelistRefresh(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.Nodelist != nil {
		s.d.Nodelist.RefreshAsync()
	}
	w.WriteHeader(http.StatusAccepted)
}

func (s *Server) handleSystem(w http.ResponseWriter, r *http.Request, _ store.User) {
	writeJSON(w, 200, s.systemJSON(r.Context()))
}

func (s *Server) handleNodeStats(w http.ResponseWriter, r *http.Request, _ store.User) {
	node, ok := s.localNode(w, r)
	if !ok {
		return
	}
	if !s.amiUp() {
		s.commandError(w, errAMIDown)
		return
	}
	ctx, cancel := s.cmdContext(r)
	defer cancel()
	fields, err := s.rpt.NodeStat(ctx, node)
	if err != nil {
		s.commandError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"node": node, "stats": fields})
}

// restartAsterisk restarts Asterisk: gracefully over AMI when connected,
// otherwise (config editing available) through the root helper.
func (s *Server) restartAsterisk(w http.ResponseWriter, r *http.Request, u store.User) bool {
	ctx, cancel := s.cmdContext(r)
	defer cancel()
	var err error
	switch {
	case s.amiUp():
		err = s.rpt.RestartAsterisk(ctx)
	case s.d.ConfigEditor != nil:
		err = s.d.ConfigEditor.RestartViaHelper(ctx)
		if err != nil {
			s.configError(w, err)
			return false
		}
	default:
		err = errAMIDown
	}
	if err != nil {
		s.commandError(w, err)
		return false
	}
	s.rescanNodes()
	s.d.Logger.Printf("asterisk restart requested by %s", u.Username)
	s.audit(u, "asterisk.restart", nil)
	return true
}

func (s *Server) handleRestartAsterisk(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.restartAsterisk(w, r, u) {
		w.WriteHeader(http.StatusAccepted)
	}
}

// rescanNodes asks for the local node list to be re-read soon, after a
// reload or restart that may have changed it.
func (s *Server) rescanNodes() {
	if s.d.RescanNodes != nil {
		s.d.RescanNodes()
	}
}
