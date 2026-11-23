package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/nodecfg"
	"github.com/artw1/aslcp/internal/store"
)

func (s *Server) nodeCfgRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/config/nodes", s.authed(store.RoleAdmin, s.handleCfgNodes))
	m.HandleFunc("GET /api/config/schema", s.authed(store.RoleAdmin, s.handleCfgSchema))
	m.HandleFunc("POST /api/config/form", s.authed(store.RoleAdmin, s.handleCfgForm))
}

// cfgWorld reads the model files through the helper.
func (s *Server) cfgWorld(ctx context.Context, svc *configsvc.Service) *nodecfg.World {
	return nodecfg.Load(svc.FS(ctx))
}

// registration status commands. The reply format of the HTTP one has not been
// verified on hardware (docs/CONFIG.md); anything unclear reads "unknown".
const (
	regStatusHTTP = "rpt show registrations"
	regStatusIAX  = "iax2 show registry"
)

func (s *Server) fillRegStatus(ctx context.Context, svc *configsvc.Service, ov *nodecfg.Overview) {
	if svc.AMI == nil || !svc.AMI.State().Connected {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cache := map[string][]string{}
	for i := range ov.Nodes {
		n := &ov.Nodes[i]
		cmd := ""
		switch n.Registration.Method {
		case nodecfg.RegHTTP:
			cmd = regStatusHTTP
		case nodecfg.RegIAX:
			cmd = regStatusIAX
		default:
			continue
		}
		lines, ok := cache[cmd]
		if !ok {
			var err error
			lines, err = svc.AMI.Command(ctx, cmd)
			if err != nil {
				lines = nil
			}
			cache[cmd] = lines
		}
		st := nodecfg.ParseRegStatus(n.Registration.Method, n.Node, lines)
		n.Registration.Status = &st
	}
}

func (s *Server) handleCfgNodes(w http.ResponseWriter, r *http.Request, _ store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	ov := s.cfgWorld(r.Context(), svc).Analyze()
	if r.URL.Query().Get("status") == "1" {
		s.fillRegStatus(r.Context(), svc, ov)
	}
	writeJSON(w, 200, ov)
}

func (s *Server) handleCfgSchema(w http.ResponseWriter, _ *http.Request, _ store.User) {
	writeJSON(w, 200, nodecfg.SchemaDoc())
}

type cfgFormReq struct {
	nodecfg.Request
	DryRun bool `json:"dryRun"`
	// Base maps files to the sha256 of the dry run the apply follows.
	Base map[string]string `json:"base"`
	// Restart: restart Asterisk after applying when the plan needs one.
	Restart bool   `json:"restart"`
	Message string `json:"message"`
}

type cfgFormResult struct {
	Plan   *nodecfg.Plan          `json:"plan"`
	Result *configsvc.ApplyResult `json:"result,omitempty"`
	// Restarted: Asterisk was restarted. RestartPending: the plan needs a
	// restart and none was asked for.
	Restarted      bool `json:"restarted"`
	RestartPending bool `json:"restartPending"`
}

func (s *Server) cfgFormError(w http.ResponseWriter, err error) {
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

func (s *Server) handleCfgForm(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in cfgFormReq
	if !decodeMax(w, r, &in, false, maxConfigBody) {
		return
	}
	ctx := r.Context()
	world := s.cfgWorld(ctx, svc)
	plan, err := world.Plan(in.Request)
	if err != nil {
		s.cfgFormError(w, err)
		return
	}
	if in.DryRun {
		writeJSON(w, 200, cfgFormResult{Plan: plan, RestartPending: plan.Restart})
		return
	}
	if plan.Blocked() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the change would break the configuration", "code": "blocked", "plan": plan})
		return
	}
	var stale []string
	for f, b := range in.Base {
		if cur, ok := plan.Base[f]; ok && b != "" && b != cur {
			stale = append(stale, f)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a file changed on disk since the plan was made: plan again", "code": "conflict", "files": stale})
		return
	}
	if plan.Noop {
		writeJSON(w, 200, cfgFormResult{Plan: plan})
		return
	}
	msg := in.Message
	if msg == "" {
		msg = "Form: node " + plan.Node
	}
	// With a restart coming, the reload steps are pointless: the restart
	// applies everything.
	res, err := svc.Apply(ctx, u.Username, plan.ConfigFiles(), msg, !(plan.Restart && in.Restart))
	if err != nil {
		s.cfgFormError(w, err)
		return
	}
	out := cfgFormResult{Plan: plan, Result: &res}
	if plan.Restart && in.Restart {
		if err := s.restartAsteriskNow(ctx); err != nil {
			s.audit(u, "config.form.failed", map[string]any{"node": plan.Node, "step": "restart"})
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the files were saved, but Asterisk did not restart: " + err.Error(), "code": "apply_failed", "step": "asterisk_restart", "applied": true, "result": res})
			return
		}
		out.Restarted = true
		if err := s.waitAsteriskBack(ctx); err != nil {
			s.audit(u, "config.form.failed", map[string]any{"node": plan.Node, "step": "asterisk_back"})
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "apply_failed", "step": "asterisk_restart", "applied": true, "result": res})
			return
		}
		h := svc.HealthCheck(ctx, []string{plan.Node})
		res.Health = &h
		out.Result = &res
	} else if plan.Restart {
		out.RestartPending = true
	}
	s.rescanNodes()
	ids := make([]string, 0, len(plan.Changes))
	for _, c := range plan.Changes {
		if c.ID != "" && c.Action != "unchanged" {
			ids = append(ids, c.ID)
		}
	}
	s.audit(u, "config.form", map[string]any{"node": plan.Node, "mode": plan.Mode, "files": res.Files, "keys": ids, "commit": res.Commit, "restart": out.Restarted})
	s.d.Logger.Printf("config form applied by %s: node %s, %d file(s), commit %s", u.Username, plan.Node, len(res.Files), res.Commit)
	writeJSON(w, 200, out)
}
