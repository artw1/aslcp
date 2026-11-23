package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/nodecfg"
	"github.com/artw1/aslcp/internal/store"
)

// "Set up this node" wizard: plan, skip flag and the pending AMI secret. The
// apply and rollback, with the fixed order of steps, are in nodesetup_run.go.

const (
	// settingSetupSkipped is the settings key of the wizard's Skip.
	settingSetupSkipped = "setup_wizard_skipped"
	// setupSecretTTL is how long a generated AMI secret waits for its apply.
	setupSecretTTL = 30 * time.Minute
)

// SetupHost is the part of the helper the wizard's apply needs besides the
// config files (helper.Client and helper.DemoSystem implement it).
type SetupHost interface {
	SavenodeSet(ctx context.Context, req helper.NodeSetupRequest) (helper.NodeSetupResult, error)
	AllmonNodeSet(ctx context.Context, req helper.NodeSetupRequest) (helper.NodeSetupResult, error)
}

// AMILoginReloader makes ASLCP's own AMI client use the credentials now in
// manager.conf. It reports whether it did: with a literal secret in
// aslcp.conf it must not, and says so.
type AMILoginReloader func(reconnect bool) (reloaded bool, err error)

type setupState struct {
	mu      sync.Mutex
	secrets map[string]pendingSecret
	reload  AMILoginReloader
}

type pendingSecret struct {
	secret string
	exp    time.Time
}

// SetAMILoginReloader is called by main once the AMI client exists.
func (s *Server) SetAMILoginReloader(f AMILoginReloader) {
	s.setup.mu.Lock()
	s.setup.reload = f
	s.setup.mu.Unlock()
}

func (s *Server) amiLoginReloader() AMILoginReloader {
	s.setup.mu.Lock()
	defer s.setup.mu.Unlock()
	return s.setup.reload
}

// newSetupSecret generates an AMI secret and keeps it server side; the
// browser only ever holds the token.
func (s *Server) newSetupSecret() (token, secret string, err error) {
	if secret, err = nodecfg.GenerateSecret(); err != nil {
		return "", "", err
	}
	var b [12]byte
	if _, err = rand.Read(b[:]); err != nil {
		return "", "", err
	}
	token = hex.EncodeToString(b[:])
	s.setup.mu.Lock()
	defer s.setup.mu.Unlock()
	if s.setup.secrets == nil {
		s.setup.secrets = map[string]pendingSecret{}
	}
	now := time.Now()
	for k, v := range s.setup.secrets {
		if now.After(v.exp) {
			delete(s.setup.secrets, k)
		}
	}
	s.setup.secrets[token] = pendingSecret{secret, now.Add(setupSecretTTL)}
	return token, secret, nil
}

func (s *Server) setupSecret(token string) (string, bool) {
	s.setup.mu.Lock()
	defer s.setup.mu.Unlock()
	v, ok := s.setup.secrets[token]
	if !ok || time.Now().After(v.exp) {
		return "", false
	}
	return v.secret, true
}

func (s *Server) dropSetupSecret(token string) {
	s.setup.mu.Lock()
	delete(s.setup.secrets, token)
	s.setup.mu.Unlock()
}

func (s *Server) setupRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/config/setup/skip", s.authed(store.RoleAdmin, s.handleSetupSkipGet))
	m.HandleFunc("PUT /api/config/setup/skip", s.authed(store.RoleAdmin, s.handleSetupSkipPut))
	m.HandleFunc("POST /api/config/setup/plan", s.authed(store.RoleAdmin, s.handleSetupPlan))
	m.HandleFunc("POST /api/config/setup/apply", s.authed(store.RoleAdmin, s.handleSetupApply))
	m.HandleFunc("POST /api/config/setup/rollback", s.authed(store.RoleAdmin, s.handleSetupRollback))
}

func (s *Server) setupSkipped() bool {
	if s.d.Store == nil {
		return false
	}
	v, err := s.d.Store.GetSetting(settingSetupSkipped)
	return err == nil && v == "1"
}

func (s *Server) handleSetupSkipGet(w http.ResponseWriter, _ *http.Request, _ store.User) {
	writeJSON(w, 200, map[string]bool{"skipped": s.setupSkipped()})
}

func (s *Server) handleSetupSkipPut(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Skipped bool `json:"skipped"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	if s.d.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "settings are not available")
		return
	}
	v := "0"
	if in.Skipped {
		v = "1"
	}
	if err := s.d.Store.SetSetting(settingSetupSkipped, v); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "could not save the setting")
		return
	}
	s.audit(u, "config.setup.skip", map[string]any{"skipped": in.Skipped})
	writeJSON(w, 200, map[string]bool{"skipped": in.Skipped})
}

// setupBody is the body of plan and apply.
type setupBody struct {
	nodecfg.SetupRequest
	// AMIToken names the AMI secret the plan generated.
	AMIToken string `json:"amiToken"`
	// Base maps files to the sha256 of the plan the apply follows.
	Base    map[string]string `json:"base"`
	Message string            `json:"message"`
}

type setupPlanResult struct {
	Plan     *nodecfg.SetupPlan `json:"plan"`
	AMIToken string             `json:"amiToken,omitempty"`
}

func (s *Server) setupError(w http.ResponseWriter, err error) {
	var (
		ie *nodecfg.InvalidError
		re *nodecfg.RefuseError
	)
	switch {
	case errors.As(err, &ie):
		writeErr(w, http.StatusBadRequest, "bad_request", ie.Msg)
	case errors.As(err, &re):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": re.Message, "code": "refused", "refusal": re.Refusal})
	default:
		s.configError(w, err)
	}
}

func (s *Server) handleSetupPlan(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in setupBody
	if !decodeMax(w, r, &in, false, maxConfigBody) {
		return
	}
	token := in.AMIToken
	if in.AMI {
		// a fresh secret for every plan unless the client keeps its token
		if sec, ok := s.setupSecret(token); ok {
			in.AMISecret = sec
		} else {
			var err error
			if token, in.AMISecret, err = s.newSetupSecret(); err != nil {
				writeErr(w, http.StatusInternalServerError, "internal", "could not generate a secret")
				return
			}
		}
	} else {
		token = ""
	}
	plan, err := s.cfgWorld(r.Context(), svc).PlanSetup(in.SetupRequest)
	if err != nil {
		s.setupError(w, err)
		return
	}
	writeJSON(w, 200, setupPlanResult{Plan: plan, AMIToken: token})
}
