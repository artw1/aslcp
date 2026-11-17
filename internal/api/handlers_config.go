package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// maxConfigBody keeps a re-encoded request under the helper's 4 MiB cap.
const maxConfigBody = 2 << 20

var commitRe = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func (s *Server) configRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/config/files", s.authed(store.RoleAdmin, s.handleConfigFiles))
	m.HandleFunc("GET /api/config/file", s.authed(store.RoleAdmin, s.handleConfigFile))
	m.HandleFunc("POST /api/config/preview", s.authed(store.RoleAdmin, s.handleConfigPreview))
	m.HandleFunc("POST /api/config/apply", s.authed(store.RoleAdmin, s.handleConfigApply))
	m.HandleFunc("GET /api/config/history", s.authed(store.RoleAdmin, s.handleConfigHistory))
	m.HandleFunc("GET /api/config/history/{commit}", s.authed(store.RoleAdmin, s.handleConfigShow))
	m.HandleFunc("POST /api/config/history/{commit}/restore", s.authed(store.RoleAdmin, s.handleConfigRestore))
	m.HandleFunc("POST /api/system/asterisk/restart", s.authed(store.RoleAdmin, s.handleAsteriskRestart))
}

// audit records an action; detail must never contain secret values or file
// contents. Failures are logged only.
func (s *Server) audit(u store.User, action string, detail map[string]any) {
	if s.d.Store == nil {
		return
	}
	d := ""
	if detail != nil {
		b, _ := json.Marshal(detail)
		d = string(b)
	}
	if err := s.d.Store.AddAudit(store.Audit{Username: u.Username, Action: action, Detail: d}); err != nil {
		s.d.Logger.Printf("audit: %v", err)
	}
}

func (s *Server) configSvc(w http.ResponseWriter) *configsvc.Service {
	if s.d.ConfigEditor == nil {
		writeErr(w, http.StatusServiceUnavailable, "config_unavailable", "configuration editing is not available")
		return nil
	}
	return s.d.ConfigEditor
}

func (s *Server) configError(w http.ResponseWriter, err error) {
	var (
		ce *helper.ConflictError
		se *configsvc.SyntaxErrors
		ie *configsvc.InvalidError
		ne *configsvc.NotFoundError
		ue *helper.UnavailableError
	)
	switch {
	case errors.As(err, &ce):
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "files changed on disk since they were loaded", "code": "conflict", "files": ce.Files})
	case errors.As(err, &se):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{
			"error": se.Error(), "code": "syntax_error", "issues": se.Issues})
	case errors.As(err, &ie):
		writeErr(w, http.StatusBadRequest, "bad_request", ie.Msg)
	case errors.As(err, &ne):
		writeErr(w, http.StatusNotFound, "not_found", ne.Msg)
	case errors.Is(err, helper.ErrBusy):
		writeErr(w, http.StatusConflict, "busy", "another change is running")
	case errors.As(err, &ue):
		writeErr(w, http.StatusServiceUnavailable, "config_unavailable", "configuration editing is not available: "+ue.Reason)
	default:
		s.d.Logger.Printf("config: %v", err)
		writeErr(w, http.StatusBadGateway, "helper_error", err.Error())
	}
}

func (s *Server) handleConfigFiles(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	files, err := svc.Files(r.Context())
	if err != nil {
		s.configError(w, err)
		return
	}
	writeJSON(w, 200, files)
}

func (s *Server) handleConfigFile(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	p := r.URL.Query().Get("path")
	c, err := svc.Read(r.Context(), p)
	if err != nil {
		s.configError(w, err)
		return
	}
	s.audit(u, "config.read", map[string]any{"path": p})
	writeJSON(w, 200, c)
}

type configBody struct {
	Files   []configsvc.File `json:"files"`
	Message string           `json:"message"`
	Reload  bool             `json:"reload"`
}

func (s *Server) handleConfigPreview(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var b configBody
	if !decodeMax(w, r, &b, false, maxConfigBody) {
		return
	}
	p, err := svc.Preview(r.Context(), b.Files)
	if err != nil {
		s.configError(w, err)
		return
	}
	writeJSON(w, 200, p)
}

func paths(files []configsvc.File) []string {
	out := make([]string, 0, len(files))
	for _, f := range files {
		out = append(out, f.Path)
	}
	return out
}

func (s *Server) handleConfigApply(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var b configBody
	if !decodeMax(w, r, &b, false, maxConfigBody) {
		return
	}
	res, err := svc.Apply(r.Context(), u.Username, b.Files, b.Message, b.Reload)
	if err != nil {
		s.configError(w, err)
		return
	}
	if b.Reload {
		s.rescanNodes()
	}
	s.audit(u, "config.apply", map[string]any{"files": res.Files, "commit": res.Commit, "before": res.Before, "reload": b.Reload})
	s.d.Logger.Printf("config applied by %s: %d file(s), commit %s", u.Username, len(res.Files), res.Commit)
	writeJSON(w, 200, res)
}

func intParam(r *http.Request, name string, def int) (int, bool) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, true
	}
	n, err := strconv.Atoi(v)
	return n, err == nil
}

func (s *Server) handleConfigHistory(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	limit, ok1 := intParam(r, "limit", 50)
	offset, ok2 := intParam(r, "offset", 0)
	if !ok1 || !ok2 {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid limit or offset")
		return
	}
	l, err := svc.History(r.Context(), limit, offset)
	if err != nil {
		s.configError(w, err)
		return
	}
	writeJSON(w, 200, l)
}

func (s *Server) handleConfigShow(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	c := r.PathValue("commit")
	if !commitRe.MatchString(c) {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid commit")
		return
	}
	reveal := r.URL.Query().Get("reveal") == "1"
	res, err := svc.Show(r.Context(), c, reveal)
	if err != nil {
		s.configError(w, err)
		return
	}
	if reveal {
		s.audit(u, "config.reveal", map[string]any{"commit": c})
	}
	writeJSON(w, 200, res)
}

func (s *Server) handleConfigRestore(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	c := r.PathValue("commit")
	if !commitRe.MatchString(c) {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid commit")
		return
	}
	var b struct {
		Reload bool `json:"reload"`
	}
	if !decode(w, r, &b, true) {
		return
	}
	res, err := svc.Restore(r.Context(), u.Username, c, b.Reload)
	if err != nil {
		s.configError(w, err)
		return
	}
	if b.Reload {
		s.rescanNodes()
	}
	s.audit(u, "config.restore", map[string]any{"from": c, "files": res.Files, "commit": res.Commit, "before": res.Before, "reload": b.Reload})
	writeJSON(w, 200, res)
}

func (s *Server) handleAsteriskRestart(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.restartAsterisk(w, r, u) {
		writeJSON(w, 200, map[string]bool{"ok": true})
	}
}
