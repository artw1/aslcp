package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/schedules"
	"github.com/artw1/aslcp/internal/store"
)

// initSchedules wires the schedule engine to this server: its commands go
// through the same helpers as the HTTP handlers (shared rpt.Client pacing),
// its changes to the SSE hub.
func (s *Server) initSchedules() {
	e := s.d.Schedules
	if e == nil {
		return
	}
	if e.Linker == nil {
		e.Linker = schedLinker{s}
	}
	e.OnChange = s.broadcastSchedules
	if e.Logger == nil {
		e.Logger = s.d.Logger
	}
}

// pauseSchedules is called after a manual link command (never by the
// scheduler's own commands): it cancels the running occurrences and pauses
// all schedules.
func (s *Server) pauseSchedules(reason string) {
	if s.d.Schedules == nil {
		return
	}
	s.d.Logger.Printf("schedules paused: %s", reason)
	s.d.Schedules.Pause(reason)
}

// schedLinker adapts the server to schedules.Linker.
type schedLinker struct{ s *Server }

func connectMode(mode string) rpt.ConnectMode {
	switch mode {
	case schedules.ModeMonitor:
		return rpt.ConnMonitor
	case schedules.ModeLocalMonitor:
		return rpt.ConnLocalMonitor
	}
	return rpt.ConnTransceive
}

func (l schedLinker) Connect(ctx context.Context, node, target, mode string, permanent, exclusive bool) error {
	return l.s.connectLink(ctx, node, target, connectMode(mode), permanent, exclusive)
}

func (l schedLinker) Disconnect(ctx context.Context, node, target string, permanent bool) error {
	return l.s.disconnectLink(ctx, node, target, permanent)
}

func (l schedLinker) LinkMode(node, target string) (string, bool, bool) {
	m := l.s.monitor()
	if m == nil || !l.s.isLocalNode(node) {
		return "", false, false
	}
	st, ok := m.Status(node)
	if !ok {
		return "", false, false
	}
	for _, k := range st.Links {
		if k.Node != target {
			continue
		}
		switch k.Mode {
		case rpt.ModeTransceive:
			return schedules.ModeTransceive, true, true
		case rpt.ModeMonitor:
			return schedules.ModeMonitor, true, true
		case rpt.ModeLocalMonitor:
			return schedules.ModeLocalMonitor, true, true
		}
		return "connecting", true, true
	}
	return "", false, true
}

func (s *Server) isLocalNode(node string) bool {
	if m := s.monitor(); m != nil {
		for _, n := range m.Nodes() {
			if n == node {
				return true
			}
		}
	}
	return false
}

// schedulesReady answers 503 when the feature is not wired.
func (s *Server) schedulesReady(w http.ResponseWriter) bool {
	if s.d.Schedules == nil || s.d.Store == nil {
		writeErr(w, 503, "schedules_unavailable", "schedules are not available")
		return false
	}
	return true
}

func (s *Server) handleSchedList(w http.ResponseWriter, r *http.Request, _ store.User) {
	if !s.schedulesReady(w) {
		return
	}
	writeJSON(w, 200, s.d.Schedules.Snapshot())
}

// schedInput is the body of POST and PUT /api/schedules.
type schedInput struct {
	Name      string `json:"name"`
	Enabled   *bool  `json:"enabled"` // absent means true
	Node      string `json:"node"`
	Target    string `json:"target"`
	Action    string `json:"action"`
	Mode      string `json:"mode"`
	Permanent bool   `json:"permanent"`
	Exclusive bool   `json:"exclusive"`
	Days      []int  `json:"days"`
	Date      string `json:"date"`
	Start     string `json:"start"`
	End       string `json:"end"`
}

// parseSchedule validates a request body into a Schedule; it writes the 400
// itself.
func (s *Server) parseSchedule(w http.ResponseWriter, r *http.Request) (schedules.Schedule, bool) {
	var in schedInput
	if !decode(w, r, &in, false) {
		return schedules.Schedule{}, false
	}
	x := schedules.Schedule{Name: in.Name, Enabled: in.Enabled == nil || *in.Enabled, Node: in.Node, Target: in.Target,
		Action: in.Action, Mode: in.Mode, Permanent: in.Permanent, Exclusive: in.Exclusive,
		Days: in.Days, Date: in.Date, Start: in.Start, End: in.End}
	if err := x.Normalize(); err != nil {
		writeErr(w, 400, "bad_request", strings.TrimPrefix(err.Error(), schedules.ErrInvalid.Error()+": "))
		return x, false
	}
	if !s.isLocalNode(x.Node) {
		writeErr(w, 400, "unknown_node", "no such local node")
		return x, false
	}
	return x, true
}

func (s *Server) handleSchedCreate(w http.ResponseWriter, r *http.Request, u store.User) {
	if !s.schedulesReady(w) {
		return
	}
	x, ok := s.parseSchedule(w, r)
	if !ok {
		return
	}
	got, err := s.d.Store.AddSchedule(x)
	if err != nil {
		s.internal(w, err)
		return
	}
	s.d.Logger.Printf("schedule %d created (%s %s -> %s) by %s", got.ID, got.Action, got.Node, got.Target, u.Username)
	s.d.Schedules.Changed(got.ID)
	writeJSON(w, 201, got)
}

func (s *Server) handleSchedUpdate(w http.ResponseWriter, r *http.Request, u store.User) {
	if !s.schedulesReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	x, ok := s.parseSchedule(w, r)
	if !ok {
		return
	}
	x.ID = id
	got, err := s.d.Store.UpdateSchedule(x)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not_found", "no such schedule")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.d.Logger.Printf("schedule %d updated by %s", id, u.Username)
	s.d.Schedules.Changed(id)
	writeJSON(w, 200, got)
}

func (s *Server) handleSchedDelete(w http.ResponseWriter, r *http.Request, u store.User) {
	if !s.schedulesReady(w) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.d.Store.DeleteSchedule(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "not_found", "no such schedule")
		return
	}
	if err != nil {
		s.internal(w, err)
		return
	}
	s.d.Logger.Printf("schedule %d deleted by %s", id, u.Username)
	s.d.Schedules.Changed(id)
	noContent(w)
}

func (s *Server) handleSchedEnabled(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	if in.Enabled == nil {
		writeErr(w, 400, "bad_request", "enabled (bool) is required")
		return
	}
	if !s.schedulesReady(w) {
		return
	}
	s.d.Logger.Printf("schedules enabled=%v by %s", *in.Enabled, u.Username)
	s.d.Schedules.SetEnabled(*in.Enabled)
	writeJSON(w, 200, s.d.Schedules.Snapshot())
}
