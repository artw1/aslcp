package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// DVSwitch is the part of the helper that reports, installs and removes
// DVSwitch (docs/DVSWITCH.md, M3).
type DVSwitch interface {
	DVSwitchStatus(ctx context.Context) (helper.DVSwitchStatus, error)
	DVSwitchJob(ctx context.Context) (helper.DVSwitchJobs, error)
	DVSwitchInstall(ctx context.Context) (helper.DVSwitchStatus, error)
	DVSwitchUninstall(ctx context.Context) (helper.DVSwitchStatus, error)
}

// dvswitchRoutes registers the DVSwitch endpoints. All are admin only: they
// install and remove system packages.
func (s *Server) dvswitchRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/dvswitch", s.authed(store.RoleAdmin, s.handleDVSwitchStatus))
	m.HandleFunc("GET /api/dvswitch/job", s.authed(store.RoleAdmin, s.handleDVSwitchJob))
	m.HandleFunc("POST /api/dvswitch/install", s.authed(store.RoleAdmin, s.handleDVSwitchInstall))
	m.HandleFunc("POST /api/dvswitch/uninstall", s.authed(store.RoleAdmin, s.handleDVSwitchUninstall))
}

func (s *Server) handleDVSwitchStatus(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	st, err := s.d.DVSwitch.DVSwitchStatus(r.Context())
	if err != nil {
		s.dvswitchError(w, err)
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleDVSwitchJob(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	j, err := s.d.DVSwitch.DVSwitchJob(r.Context())
	if err != nil {
		s.dvswitchError(w, err)
		return
	}
	writeJSON(w, 200, j)
}

func (s *Server) handleDVSwitchInstall(w http.ResponseWriter, r *http.Request, u store.User) {
	s.dvswitchStart(w, r, u, "dvswitch.install", func(ctx context.Context) (helper.DVSwitchStatus, error) {
		return s.d.DVSwitch.DVSwitchInstall(ctx)
	})
}

func (s *Server) handleDVSwitchUninstall(w http.ResponseWriter, r *http.Request, u store.User) {
	s.dvswitchStart(w, r, u, "dvswitch.uninstall", func(ctx context.Context) (helper.DVSwitchStatus, error) {
		return s.d.DVSwitch.DVSwitchUninstall(ctx)
	})
}

func (s *Server) dvswitchStart(w http.ResponseWriter, r *http.Request, u store.User, action string, f func(context.Context) (helper.DVSwitchStatus, error)) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	st, err := f(r.Context())
	if err != nil {
		s.audit(u, action+".refused", map[string]any{"error": err.Error()})
		s.dvswitchError(w, err)
		return
	}
	s.audit(u, action, nil)
	s.d.Logger.Printf("%s started by %s", action, u.Username)
	writeJSON(w, http.StatusAccepted, st)
}

var errDVUnavailable = errors.New("dvswitch unavailable")

func (s *Server) dvswitchError(w http.ResponseWriter, err error) {
	var ue *helper.UnavailableError
	switch {
	case errors.Is(err, errDVUnavailable):
		writeErr(w, http.StatusServiceUnavailable, "dvswitch_unavailable", "DVSwitch management is not available")
	case errors.As(err, &ue):
		writeErr(w, http.StatusServiceUnavailable, "dvswitch_unavailable", "DVSwitch management is not available: "+ue.Reason)
	case errors.Is(err, helper.ErrBusy):
		writeErr(w, http.StatusConflict, "job_running", "an install, uninstall or update is already running")
	case errors.Is(err, helper.ErrAptBusy):
		writeErr(w, http.StatusConflict, "apt_busy", "apt is busy, try again later")
	case errors.Is(err, helper.ErrDVAlreadyInstalled):
		writeErr(w, http.StatusConflict, "already_installed", err.Error())
	case errors.Is(err, helper.ErrDVUnsupported):
		writeErr(w, http.StatusConflict, "unsupported_platform", err.Error())
	case errors.Is(err, helper.ErrDVClockUnset):
		writeErr(w, http.StatusConflict, "clock_unset", err.Error())
	case errors.Is(err, helper.ErrDVOutsideASLCP):
		writeErr(w, http.StatusConflict, "not_aslcp_installed", err.Error())
	case errors.Is(err, helper.ErrDVKeyMismatch):
		s.d.Logger.Printf("dvswitch: %v", err)
		writeErr(w, http.StatusBadGateway, "key_mismatch", err.Error())
	default:
		s.d.Logger.Printf("dvswitch: %v", err)
		writeErr(w, http.StatusBadGateway, "helper_error", err.Error())
	}
}
