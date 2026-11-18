package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/artw1/aslcp/internal/dvswitch"
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
	// Credentials and ini config (M4).
	DVSwitchSetup(ctx context.Context) (helper.DVSwitchSetup, error)
	DVSwitchInfo(ctx context.Context) (helper.DVSwitchInfo, error)
	// DMR login state from MMDVM_Bridge's log (read-only, no secret).
	DVSwitchDMRStatus(ctx context.Context) (helper.DVDMRStatus, error)
	DVSwitchIdentity(ctx context.Context, req helper.DVSwitchRequest) (helper.DVApplyResult, error)
	DVSwitchCredentials(ctx context.Context, req helper.DVSwitchRequest) (helper.DVApplyResult, error)
	DVSwitchModes(ctx context.Context, req helper.DVSwitchRequest) (helper.DVApplyResult, error)
	DVSwitchStartup(ctx context.Context, req helper.DVSwitchRequest) (helper.DVApplyResult, error)
	DVSwitchRollback(ctx context.Context, req helper.DVSwitchRequest) (helper.DVApplyResult, error)
	DVSwitchRestart(ctx context.Context, req helper.DVSwitchRequest) (helper.DVRestartResult, error)
	// Host and TG lists of the pickers (read-only).
	DVSwitchLists(ctx context.Context, mode string) (helper.DVList, error)
	// Private bridge node (M2): the ini's [USRP] address and ports, and the
	// record of what ASLCP changed.
	DVSwitchUSRP(ctx context.Context, req helper.DVSwitchRequest) (helper.DVApplyResult, error)
	DVSwitchPrivate(ctx context.Context, p helper.DVPrivate) (helper.DVPrivateReply, error)
	// Mode switch and tune.
	DVSwitchTune(ctx context.Context, req helper.DVSwitchRequest) (helper.DVTuneResult, error)
	// The BrandMeister connection (STFU) and the STFU package.
	DVSwitchBM(ctx context.Context, req helper.DVSwitchRequest) (helper.DVApplyResult, error)
	DVSwitchSTFUInstall(ctx context.Context) (helper.DVSwitchStatus, error)
}

// DVResume is the tracker of the last tune per mode and the "resume after
// restart" setting (internal/dvswitch).
type DVResume interface {
	Get() dvswitch.View
	SetResume(on bool) error
}

// dvswitchRoutes registers the DVSwitch endpoints. Install, config and
// restart are admin only (they change system packages and files); the lists,
// the DMR login state, the tune and the remembered tunes are for operators.
func (s *Server) dvswitchRoutes() {
	s.dvswitchCtlRoutes()
	s.dvPrivateRoutes()
	m := s.mux
	m.HandleFunc("GET /api/dvswitch", s.authed(store.RoleAdmin, s.handleDVSwitchStatus))
	m.HandleFunc("GET /api/dvswitch/job", s.authed(store.RoleAdmin, s.handleDVSwitchJob))
	m.HandleFunc("POST /api/dvswitch/install", s.authed(store.RoleAdmin, s.handleDVSwitchInstall))
	m.HandleFunc("POST /api/dvswitch/uninstall", s.authed(store.RoleAdmin, s.handleDVSwitchUninstall))
	// The lists are public reflector data: any operator may read them (M1).
	m.HandleFunc("GET /api/dvswitch/lists", s.authed(store.RoleOperator, s.handleDVSwitchLists))
	// The DMR login state holds no secret: operators may read it too.
	m.HandleFunc("GET /api/dvswitch/dmr-status", s.authed(store.RoleOperator, s.handleDVSwitchDMRStatus))
	m.HandleFunc("GET /api/dvswitch/setup", s.authed(store.RoleAdmin, s.handleDVSwitchSetup))
	m.HandleFunc("PUT /api/dvswitch/identity", s.authed(store.RoleAdmin, s.handleDVSwitchIdentity))
	m.HandleFunc("PUT /api/dvswitch/credentials", s.authed(store.RoleAdmin, s.handleDVSwitchCredentials))
	m.HandleFunc("PUT /api/dvswitch/modes", s.authed(store.RoleAdmin, s.handleDVSwitchModes))
	m.HandleFunc("PUT /api/dvswitch/startup", s.authed(store.RoleAdmin, s.handleDVSwitchStartup))
	m.HandleFunc("PUT /api/dvswitch/brandmeister", s.authed(store.RoleAdmin, s.handleDVSwitchBMSave))
	m.HandleFunc("DELETE /api/dvswitch/brandmeister", s.authed(store.RoleAdmin, s.handleDVSwitchBMClear))
	m.HandleFunc("DELETE /api/dvswitch/dmr-connection", s.authed(store.RoleAdmin, s.handleDVSwitchDMRClear))
	m.HandleFunc("POST /api/dvswitch/stfu/install", s.authed(store.RoleAdmin, s.handleDVSwitchSTFUInstall))
	// Tune and resume (M1): operators tune, only admins change the setting.
	m.HandleFunc("POST /api/dvswitch/tune", s.authed(store.RoleOperator, s.handleDVSwitchTune))
	m.HandleFunc("GET /api/dvswitch/resume", s.authed(store.RoleOperator, s.handleDVSwitchResume))
	m.HandleFunc("PUT /api/dvswitch/resume", s.authed(store.RoleAdmin, s.handleDVSwitchResumeSet))
	m.HandleFunc("POST /api/dvswitch/restart", s.authed(store.RoleAdmin, s.handleDVSwitchRestart))
	m.HandleFunc("POST /api/dvswitch/rollback", s.authed(store.RoleAdmin, s.handleDVSwitchRollback))
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

func (s *Server) handleDVSwitchDMRStatus(w http.ResponseWriter, r *http.Request, _ store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	st, err := s.d.DVSwitch.DVSwitchDMRStatus(r.Context())
	if err != nil {
		s.dvswitchError(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
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

func (s *Server) handleDVSwitchSTFUInstall(w http.ResponseWriter, r *http.Request, u store.User) {
	s.dvswitchStart(w, r, u, "dvswitch.stfu.install", func(ctx context.Context) (helper.DVSwitchStatus, error) {
		return s.d.DVSwitch.DVSwitchSTFUInstall(ctx)
	})
}

func (s *Server) handleDVSwitchUninstall(w http.ResponseWriter, r *http.Request, u store.User) {
	s.dvswitchStart(w, r, u, "dvswitch.uninstall", func(ctx context.Context) (helper.DVSwitchStatus, error) {
		// A permanent link goes first (the reconciler's own command, not a
		// manual one); the helper's unit then removes the node and restarts
		// Asterisk (docs/DVSWITCH.md, "Uninstall").
		if s.d.DVLink != nil {
			if err := s.d.DVLink.Unlink(ctx); err != nil {
				s.d.Logger.Printf("dvswitch uninstall: cannot remove the permanent link: %v", err)
			}
		}
		st, err := s.d.DVSwitch.DVSwitchUninstall(ctx)
		if err == nil && s.d.Store != nil {
			// no node left to link to: the reconciler must not retry
			_ = dvswitch.WriteLinkSettings(s.d.Store, dvswitch.LinkSettings{Mode: dvswitch.LinkOnDemand, Exclusive: s.dvLinkSettings().Exclusive})
		}
		return st, err
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
	s.dvs.invalidate()
	writeJSON(w, http.StatusAccepted, st)
}

var errDVUnavailable = errors.New("dvswitch unavailable")

func (s *Server) dvswitchError(w http.ResponseWriter, err error) {
	var (
		ue *helper.UnavailableError
		ce *helper.ConflictError
	)
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
	case errors.Is(err, helper.ErrDVInvalid):
		// the helper names file/section/key, never a value
		writeErr(w, http.StatusBadRequest, "invalid_field", err.Error())
	case errors.Is(err, helper.ErrDVNotInstalled):
		writeErr(w, http.StatusConflict, "not_installed", err.Error())
	case errors.Is(err, helper.ErrDVNoHistory):
		writeErr(w, http.StatusNotFound, "no_history", err.Error())
	case errors.Is(err, helper.ErrDVNoList):
		writeErr(w, http.StatusNotFound, "no_list", err.Error())
	case errors.Is(err, helper.ErrDVNoPorts):
		writeErr(w, http.StatusConflict, "no_ports", err.Error())
	case errors.Is(err, helper.ErrDVNoInfo):
		writeErr(w, http.StatusConflict, "no_info", err.Error())
	case errors.Is(err, helper.ErrDVSTFUNotInstalled):
		writeErr(w, http.StatusConflict, "stfu_not_installed", err.Error())
	case errors.Is(err, helper.ErrDVSTFUInstalled):
		writeErr(w, http.StatusConflict, "stfu_installed", err.Error())
	case errors.Is(err, helper.ErrDVNoIdentity):
		writeErr(w, http.StatusConflict, "no_identity", err.Error())
	case errors.Is(err, helper.ErrDVNoPassword):
		writeErr(w, http.StatusConflict, "no_password", err.Error())
	case errors.Is(err, helper.ErrDVBMMaster):
		writeErr(w, http.StatusBadRequest, "bm_master", err.Error())
	case errors.Is(err, helper.ErrDVSwitchFailed):
		s.d.Logger.Printf("dvswitch: %v", err)
		writeErr(w, http.StatusBadGateway, "switch_failed", err.Error())
	case errors.As(err, &ce):
		writeErr(w, http.StatusConflict, "conflict", "a file changed on disk since it was read: reload and try again")
	case errors.Is(err, helper.ErrDVKeyMismatch):
		s.d.Logger.Printf("dvswitch: %v", err)
		writeErr(w, http.StatusBadGateway, "key_mismatch", err.Error())
	default:
		s.d.Logger.Printf("dvswitch: %v", err)
		writeErr(w, http.StatusBadGateway, "helper_error", err.Error())
	}
}
