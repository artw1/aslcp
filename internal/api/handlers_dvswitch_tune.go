package api

import (
	"net/http"

	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// Tune and resume (docs/DVSWITCH.md, "Tune and resume").

// handleDVSwitchTune: POST /api/dvswitch/tune {mode, tune}. The helper
// validates both per mode; the audit log gets the mode and the value only
// when they passed the same validators (a TG or a reflector is public data).
func (s *Server) handleDVSwitchTune(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	var b struct {
		Mode string `json:"mode"`
		Tune string `json:"tune"`
	}
	if !decode(w, r, &b, false) {
		return
	}
	fields := map[string]any{"mode": "?"}
	if helper.DVValidTune(b.Mode, b.Tune) {
		fields = map[string]any{"mode": b.Mode, "tune": b.Tune}
	}
	res, err := s.d.DVSwitch.DVSwitchTune(r.Context(), helper.DVSwitchRequest{Author: u.Username, Tune: &helper.DVTune{Mode: b.Mode, Tune: b.Tune}})
	if err != nil {
		s.audit(u, "dvswitch.tune.refused", fields)
		s.dvswitchError(w, err)
		return
	}
	s.audit(u, "dvswitch.tune", fields)
	s.d.Logger.Printf("dvswitch.tune %s by %s", res.Mode, u.Username)
	writeJSON(w, 200, res)
}

// handleDVSwitchResume: GET /api/dvswitch/resume, the last tune of each mode
// and the setting. No secret in it.
func (s *Server) handleDVSwitchResume(w http.ResponseWriter, _ *http.Request, _ store.User) {
	if s.d.DVResume == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, 200, s.d.DVResume.Get())
}

// handleDVSwitchResumeSet: PUT /api/dvswitch/resume {resume: bool}, admin.
func (s *Server) handleDVSwitchResumeSet(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVResume == nil {
		s.dvswitchError(w, errDVUnavailable)
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
		s.d.Logger.Printf("dvswitch.resume: %v", err)
		writeErr(w, http.StatusInternalServerError, "internal", "could not save the setting")
		return
	}
	s.audit(u, "dvswitch.resume", map[string]any{"resume": *b.Resume})
	s.d.Logger.Printf("dvswitch.resume=%t by %s", *b.Resume, u.Username)
	writeJSON(w, 200, s.d.DVResume.Get())
}
