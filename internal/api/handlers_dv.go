package api

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/hostlists"
	"github.com/artw1/aslcp/internal/store"
)

// DVResume is the tracker of the last tune per mode and the "resume after
// restart" setting (internal/dvpanel).
type DVResume interface {
	Get() dvpanel.View
	SetResume(on bool) error
}

// dvRoutes registers the digital modes endpoints. Enable, disable, setup and
// the private node are admin only (they change system packages and files);
// the lists, the tune and the remembered tunes are for operators.
func (s *Server) dvRoutes() {
	s.dvCtlRoutes()
	s.dvPrivateRoutes()
	s.dvNetRoutes()
	m := s.mux
	m.HandleFunc("GET /api/dv", s.authed(store.RoleViewer, s.handleDVSnapshot))
	m.HandleFunc("GET /api/dv/status", s.authed(store.RoleAdmin, s.handleDVStatus))
	m.HandleFunc("GET /api/dv/job", s.authed(store.RoleAdmin, s.handleDVJob))
	m.HandleFunc("POST /api/dv/enable", s.authed(store.RoleAdmin, s.handleDVEnable))
	m.HandleFunc("POST /api/dv/disable", s.authed(store.RoleAdmin, s.handleDVDisable))
	m.HandleFunc("GET /api/dv/setup", s.authed(store.RoleAdmin, s.handleDVSetup))
	m.HandleFunc("GET /api/dv/lists", s.authed(store.RoleOperator, s.handleDVLists))
	m.HandleFunc("POST /api/dv/lists/refresh", s.authed(store.RoleAdmin, s.handleDVListsRefresh))
	m.HandleFunc("GET /api/dv/resume", s.authed(store.RoleOperator, s.handleDVResume))
	m.HandleFunc("PUT /api/dv/resume", s.authed(store.RoleAdmin, s.handleDVResumeSet))
	m.HandleFunc("POST /api/dv/rollback", s.authed(store.RoleAdmin, s.handleDVPrivateRollback))
}

// DVStatusView is the reply of GET status, enable and disable.
type DVStatusView struct {
	Supported      bool     `json:"supported"`
	Reason         string   `json:"reason,omitempty"`
	MD380Installed bool     `json:"md380Installed"`
	Enabled        bool     `json:"enabled"`
	Active         bool     `json:"active"`
	MD380Active    bool     `json:"md380Active"`
	Job            string   `json:"job,omitempty"`
	LegacyUnits    []string `json:"legacyUnits,omitempty"`
}

func dvStatusView(st helper.DVStatus) DVStatusView {
	return DVStatusView{Supported: st.Supported, Reason: st.Reason, MD380Installed: st.MD380Installed, Enabled: st.Enabled, Active: st.Active,
		MD380Active: st.MD380Active, Job: st.Job, LegacyUnits: st.LegacyUnits}
}

func (s *Server) handleDVStatus(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	st, err := s.d.DVEngine.DVStatus(r.Context())
	if err != nil {
		s.dvError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, dvStatusView(st))
}

func (s *Server) handleDVJob(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	j, err := s.d.DVEngine.DVJob(r.Context())
	if err != nil {
		s.dvError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, j)
}

func (s *Server) handleDVEnable(w http.ResponseWriter, r *http.Request, u store.User) {
	s.dvStart(w, r, u, "dv.enable", func(ctx context.Context) (helper.DVStatus, error) { return s.d.DVEngine.DVEnable(ctx) })
}

func (s *Server) handleDVDisable(w http.ResponseWriter, r *http.Request, u store.User) {
	s.dvStart(w, r, u, "dv.disable", func(ctx context.Context) (helper.DVStatus, error) {
		// A permanent link goes first (the reconciler's own command); the
		// private node itself stays, it is the operator's to remove.
		if s.d.DVLink != nil {
			if err := s.d.DVLink.Unlink(ctx); err != nil {
				s.d.Logger.Printf("dv disable: cannot remove the permanent link: %v", err)
			}
		}
		return s.d.DVEngine.DVDisable(ctx)
	})
}

// dvStart runs enable or disable: 202 with the status, 409 while a job runs.
func (s *Server) dvStart(w http.ResponseWriter, r *http.Request, u store.User, action string, f func(context.Context) (helper.DVStatus, error)) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	st, err := f(r.Context())
	if err != nil {
		s.audit(u, action+".refused", map[string]any{"error": err.Error()})
		s.dvError(w, err)
		return
	}
	s.audit(u, action, nil)
	s.d.Logger.Printf("%s started by %s", action, u.Username)
	s.dvs.invalidate()
	writeJSON(w, http.StatusAccepted, dvStatusView(st))
}

// DVFileView is one Asterisk file of the private node flow. SHA256 is passed
// back as the base of a write.
type DVFileView struct {
	ID       string `json:"id"`
	Present  bool   `json:"present"`
	Writable bool   `json:"writable"`
	SHA256   string `json:"sha256"`
	History  int    `json:"history"`
}

// dvSetupReply is the reply of GET setup (read-only).
type dvSetupReply struct {
	BridgeNode DVBridgeSetup `json:"bridgeNode"`
	Files      []DVFileView  `json:"files"`
}

func (s *Server) handleDVSetup(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	ctx := r.Context()
	st, err := s.dvSetupState(ctx)
	if err != nil {
		s.dvError(w, err)
		return
	}
	out := dvSetupReply{BridgeNode: s.dvBridgeSetup(ctx, st), Files: []DVFileView{}}
	var commits []helper.ConfigCommit
	if svc := s.d.ConfigEditor; svc != nil {
		commits, _ = svc.History(ctx, 200, 0)
	}
	for _, id := range []string{dvRptFile, dvModulesFile} {
		v := DVFileView{ID: id}
		if svc := s.d.ConfigEditor; svc != nil {
			if c, err := svc.Read(ctx, id); err == nil {
				v.Present, v.Writable, v.SHA256 = true, true, c.SHA256
			}
		}
		for _, c := range commits {
			for _, f := range c.Files {
				if f == id {
					v.History++
					break
				}
			}
		}
		out.Files = append(out.Files, v)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, out)
}

// handleDVLists: GET lists?mode=dmr|ysf|p25|nxdn|bm|dmr-other, a host list for the
// pickers. bm is the BrandMeister masters of the DMR list (hostlists.BMMasters). dmr-other is the rest
// of it, without BrandMeister and TGIF (hostlists.OtherDMRMasters).
func (s *Server) handleDVLists(w http.ResponseWriter, r *http.Request, _ store.User) {
	mode := strings.ToLower(r.URL.Query().Get("mode"))
	bm, other := mode == "bm", mode == "dmr-other"
	if bm || other {
		mode = "dmr"
	}
	if !hostlists.ValidMode(mode) {
		writeErr(w, http.StatusBadRequest, "bad_request", "mode must be dmr, ysf, p25, nxdn, bm or dmr-other")
		return
	}
	if s.d.HostLists == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	l, err := s.d.HostLists.Get(mode)
	if err != nil {
		s.dvError(w, err)
		return
	}
	if bm {
		l = hostlists.List{Mode: "bm", Items: hostlists.BMMasters(l.Items), ETag: l.ETag}
		mode = "bm"
	}
	if other {
		l = hostlists.List{Mode: "dmr-other", Items: hostlists.OtherDMRMasters(l.Items), ETag: l.ETag}
		mode = "dmr-other"
	}
	tag := `"` + mode + "-" + l.ETag + `"`
	w.Header().Set("ETag", tag)
	w.Header().Set("Cache-Control", "private, max-age=300")
	if inm := r.Header.Get("If-None-Match"); inm != "" && inm == tag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	writeJSON(w, 200, l)
}

// handleDVListsRefresh: POST lists/refresh, admin: fetch the lists now (at
// most once every ten minutes).
func (s *Server) handleDVListsRefresh(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.HostLists == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	if err := s.d.HostLists.Refresh(r.Context()); err != nil {
		s.audit(u, "dv.lists.refresh.refused", map[string]any{"error": err.Error()})
		s.dvError(w, err)
		return
	}
	s.audit(u, "dv.lists.refresh", nil)
	writeJSON(w, 200, s.d.HostLists.Status())
}

// handleDVResume: GET resume, the last tune of each mode and the setting.
func (s *Server) handleDVResume(w http.ResponseWriter, _ *http.Request, _ store.User) {
	if s.d.DVResume == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.d.DVResume.Get())
}

// handleDVResumeSet: PUT resume {resume: bool}, admin.
func (s *Server) handleDVResumeSet(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVResume == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	var b struct {
		Resume *bool `json:"resume"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	if b.Resume == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "resume must be true or false")
		return
	}
	if err := s.d.DVResume.SetResume(*b.Resume); err != nil {
		s.d.Logger.Printf("dv.resume: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not save the setting")
		return
	}
	s.audit(u, "dv.resume", map[string]any{"resume": *b.Resume})
	s.d.Logger.Printf("dv.resume=%t by %s", *b.Resume, u.Username)
	writeJSON(w, 200, s.d.DVResume.Get())
}

var errDVUnavailable = errors.New("digital modes unavailable")

// dvError maps helper, engine and host list errors to the API's codes.
func (s *Server) dvError(w http.ResponseWriter, err error) {
	var (
		ue *helper.UnavailableError
		ce *helper.ConflictError
		ee *ctl.Error
	)
	switch {
	case errors.Is(err, errDVUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "dv_unavailable", "digital modes management is not available")
	case errors.As(err, &ue):
		writeErr(w, http.StatusServiceUnavailable, "dv_unavailable", "digital modes management is not available: "+ue.Reason)
	case errors.Is(err, helper.ErrBusy):
		writeErr(w, http.StatusConflict, "busy", "an enable, disable or update is already running")
	case errors.Is(err, helper.ErrAptBusy):
		writeErr(w, http.StatusConflict, "apt_busy", "apt is busy, try again later")
	case errors.Is(err, helper.ErrDVUnsupported):
		writeErr(w, http.StatusConflict, "not_supported", err.Error())
	case errors.Is(err, helper.ErrDVClockUnset):
		writeErr(w, http.StatusConflict, "clock_unset", err.Error())
	case errors.Is(err, helper.ErrDVNotEnabled):
		writeErr(w, http.StatusConflict, "not_enabled", err.Error())
	case errors.Is(err, helper.ErrDVUnexpectedDe):
		writeErr(w, http.StatusConflict, "unexpected_deps", err.Error())
	case errors.Is(err, helper.ErrDVNetIncomplete):
		writeErr(w, http.StatusUnprocessableEntity, "incomplete", err.Error())
	case errors.Is(err, helper.ErrDVBadRequest):
		writeErr(w, http.StatusBadRequest, "invalid_field", err.Error())
	case errors.Is(err, helper.ErrDVKeyMismatch):
		s.d.Logger.Printf("dv: %v", err)
		writeErr(w, http.StatusBadGateway, "key_mismatch", err.Error())
	case errors.Is(err, hostlists.ErrNoList):
		writeErr(w, http.StatusNotFound, "no_list", "there is no host list for this mode yet")
	case errors.Is(err, hostlists.ErrTooSoon):
		writeErr(w, http.StatusTooManyRequests, "too_soon", "the lists were refreshed a moment ago: try again in a few minutes")
	case errors.Is(err, hostlists.ErrBusy):
		writeErr(w, http.StatusConflict, "busy", "a refresh is already running")
	case errors.Is(err, hostlists.ErrDisabled):
		writeErr(w, http.StatusConflict, "not_enabled", "digital modes are not enabled")
	case errors.As(err, &ee):
		s.dvCtlError(w, ee)
	case errors.As(err, &ce):
		writeErr(w, http.StatusConflict, "conflict", "a file changed on disk since it was read: reload and try again")
	case errors.Is(err, os.ErrNotExist):
		writeErr(w, http.StatusNotFound, "not_found", "not found")
	default:
		s.d.Logger.Printf("dv: %v", err)
		writeErr(w, http.StatusBadGateway, "helper_error", err.Error())
	}
}

// dvCtlError maps an engine refusal.
func (s *Server) dvCtlError(w http.ResponseWriter, e *ctl.Error) {
	switch e.Code {
	case ctl.CodeNotAvailable:
		writeErr(w, http.StatusConflict, "not_available", "this mode is not yet available in this build")
	case ctl.CodeNotEnabled:
		writeErr(w, http.StatusConflict, "not_enabled", "this mode is not enabled")
	case ctl.CodeBusy:
		writeErr(w, http.StatusConflict, "busy", "a call is in progress")
	case ctl.CodeBadRequest, ctl.CodeForbidden:
		writeErr(w, http.StatusBadRequest, "bad_request", e.Msg)
	case ctl.CodeBadConfig:
		writeErr(w, http.StatusUnprocessableEntity, "bad_config", e.Msg)
	default:
		s.d.Logger.Printf("dv engine: %s: %s", e.Code, e.Msg)
		writeErr(w, http.StatusBadGateway, "engine_error", e.Msg)
	}
}
