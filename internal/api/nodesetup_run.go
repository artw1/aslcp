package api

import (
	"net/http"
	"regexp"
	"sort"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/nodecfg"
	"github.com/artw1/aslcp/internal/store"
)

// The order of the wizard's apply lives here and nowhere else. Each step is
// complete and ordered so that a failure leaves a state the one-click
// rollback can undo:
//
//	files      config files through the safety pipeline (conflict check,
//	           history snapshot, atomic write), no reload when a restart follows
//	savenode   /etc/asterisk/savenode.conf through the root helper
//	ami        ASLCP's own AMI login follows the new manager.conf secret
//	restart    Asterisk restart and wait until it answers again (the new
//	           login is used for the reconnect)
//	allmon3    allmon3.ini node section and pass, then try-restart of allmon3
//	health     config health check of the new node
//
// Then the node list is re-read and the audit entry is written. The
// registration check is separate: the client polls GET /api/config/nodes?status=1.

type setupStepResult struct {
	ID     string `json:"id"`
	Status string `json:"status"` // done, skipped, failed
	Detail string `json:"detail,omitempty"`
}

// setupRollback is what the client sends back to undo the apply.
type setupRollback struct {
	Commit  string `json:"commit"`
	Node    string `json:"node"`
	OldNode string `json:"oldNode"`
	AMI     bool   `json:"ami"`
	Restart bool   `json:"restart"`
	// Added: the apply added Node (add-node wizard). Rolling back removes it,
	// so the health check must not ask for it.
	Added bool `json:"added,omitempty"`
}

type setupApplyResult struct {
	Plan      *nodecfg.SetupPlan     `json:"plan"`
	Result    *configsvc.ApplyResult `json:"result,omitempty"`
	Steps     []setupStepResult      `json:"steps"`
	Restarted bool                   `json:"restarted"`
	Rollback  *setupRollback         `json:"rollback,omitempty"`
}

var reHistAuthor = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func helperAuthor(u store.User) string {
	if reHistAuthor.MatchString(u.Username) {
		return u.Username
	}
	return "aslcp"
}

func (s *Server) setupHost() SetupHost {
	h, _ := s.d.SysHost.(SetupHost)
	return h
}

func (s *Server) handleSetupApply(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in setupBody
	if !decodeMax(w, r, &in, false, maxConfigBody) {
		return
	}
	if in.AMI {
		sec, ok := s.setupSecret(in.AMIToken)
		if !ok {
			writeErr(w, http.StatusBadRequest, "bad_request", "the generated AMI secret expired: plan again")
			return
		}
		in.AMISecret = sec
	}
	ctx := r.Context()
	plan, err := s.cfgWorld(ctx, svc).PlanSetup(in.SetupRequest)
	if err != nil {
		s.setupError(w, err)
		return
	}
	s.runWizard(w, r, u, svc, wizardSpec{
		action: "config.setup", defaultMsg: "Setup wizard: node " + plan.Node, plan: plan, base: in.Base, message: in.Message,
		savenode: true, password: in.Password, ami: in.AMI, amiSecret: in.AMISecret, amiToken: in.AMIToken,
		allmon: plan.Renumber || in.AMI, logName: "setup wizard",
	})
}

// wizardSpec describes one wizard apply for runWizard: the plan and which
// of the helper steps it needs. The order of the steps is fixed here.
type wizardSpec struct {
	action     string // audit action; ".failed" is added on failure
	logName    string
	defaultMsg string
	plan       *nodecfg.SetupPlan
	base       map[string]string
	message    string
	// savenode: write NODE and PASSWORD to savenode.conf.
	savenode bool
	password string
	// ami: the plan sets a new AMI secret, ASLCP's login follows it.
	ami       bool
	amiSecret string
	amiToken  string
	// allmon: allmon3.ini follows the new number (and secret).
	allmon bool
	// added: the plan adds a node (the rollback removes it again).
	added bool
}

// runWizard checks a wizard plan against the files on disk and runs it:
// files, savenode, ami, restart, allmon3, health.
func (s *Server) runWizard(w http.ResponseWriter, r *http.Request, u store.User, svc *configsvc.Service, sp wizardSpec) {
	ctx := r.Context()
	plan := sp.plan
	if plan.Blocked() {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "the change would break the configuration", "code": "blocked", "plan": plan})
		return
	}
	var stale []string
	for f, b := range sp.base {
		if cur, ok := plan.Base[f]; ok && b != "" && b != cur {
			stale = append(stale, f)
		}
	}
	if len(stale) > 0 {
		sort.Strings(stale)
		writeJSON(w, http.StatusConflict, map[string]any{"error": "a file changed on disk since the plan was made: plan again", "code": "conflict", "files": stale})
		return
	}
	out := setupApplyResult{Plan: plan, Steps: []setupStepResult{}}
	if plan.Noop {
		writeJSON(w, 200, out)
		return
	}
	step := func(id, status, detail string) {
		out.Steps = append(out.Steps, setupStepResult{ID: id, Status: status, Detail: detail})
	}
	// fail answers after the files were written: the client gets the steps so
	// far and the rollback to offer.
	fail := func(id string, err error) {
		step(id, "failed", err.Error())
		s.audit(u, sp.action+".failed", map[string]any{"node": plan.Node, "step": id})
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "the change stopped at step " + id + ": " + err.Error(), "code": "apply_failed",
			"step": id, "applied": true, "result": out.Result, "steps": out.Steps, "rollback": out.Rollback})
	}

	msg := sp.message
	if msg == "" {
		msg = sp.defaultMsg
	}
	// 1. files
	res, err := svc.Apply(ctx, u.Username, plan.ConfigFiles(), msg, !plan.Restart)
	if err != nil {
		s.setupError(w, err)
		return
	}
	out.Result = &res
	out.Rollback = &setupRollback{Commit: res.Before, Node: plan.Node, OldNode: plan.OldNode, AMI: sp.ami, Restart: plan.Restart, Added: sp.added}
	step("files", "done", "")

	// 2. savenode.conf
	host := s.setupHost()
	switch {
	case !sp.savenode:
		step("savenode", "skipped", "not requested")
	case host == nil:
		step("savenode", "skipped", "the helper is not available")
	default:
		enable := true
		sr, err := host.SavenodeSet(ctx, helper.NodeSetupRequest{Node: plan.Node, Password: sp.password, Enable: &enable, Author: helperAuthor(u)})
		switch {
		case err != nil:
			fail("savenode", err)
			return
		case sr.Skipped != "":
			step("savenode", "skipped", "savenode.conf does not exist on this system")
		default:
			step("savenode", "done", "")
		}
	}

	// 3. ASLCP's AMI login
	if !sp.ami {
		step("ami", "skipped", "the AMI secret was not changed")
	} else if reload := s.amiLoginReloader(); reload == nil {
		step("ami", "skipped", "ASLCP's AMI connection is not managed here")
	} else if ok, err := reload(false); err != nil {
		fail("ami", err)
		return
	} else if !ok {
		step("ami", "skipped", "aslcp.conf sets the AMI secret itself: update it there to match manager.conf")
	} else {
		step("ami", "done", "")
	}

	// 4. restart
	if plan.Restart {
		if err := s.restartAsteriskNow(ctx); err != nil {
			fail("restart", err)
			return
		}
		out.Restarted = true
		if err := s.waitAsteriskBack(ctx); err != nil {
			fail("restart", err)
			return
		}
		step("restart", "done", "")
	} else {
		step("restart", "skipped", "not needed")
	}

	// 5. allmon3.ini
	if host == nil || !sp.allmon {
		step("allmon3", "skipped", "nothing to change")
	} else {
		req := helper.NodeSetupRequest{OldNode: plan.OldNode, Node: plan.Node}
		if sp.ami {
			req.AMIPass = sp.amiSecret
		}
		ar, err := host.AllmonNodeSet(ctx, req)
		switch {
		case err != nil:
			// the node itself is set up; Allmon3 can be fixed by hand
			step("allmon3", "failed", err.Error())
		case ar.Skipped != "":
			step("allmon3", "skipped", "Allmon3 is not installed")
		default:
			step("allmon3", "done", "")
		}
	}

	// 6. health
	h := svc.HealthCheck(ctx, []string{plan.Node})
	out.Result.Health = &h
	if h.OK {
		step("health", "done", "")
	} else {
		step("health", "failed", "the health check found problems")
	}

	if sp.ami {
		s.dropSetupSecret(sp.amiToken)
	}
	s.rescanNodes()
	s.audit(u, sp.action, map[string]any{"node": plan.Node, "oldNode": plan.OldNode, "files": res.Files, "commit": res.Commit,
		"restart": out.Restarted, "ami": sp.ami, "steps": stepSummary(out.Steps)})
	s.d.Logger.Printf("%s applied by %s: node %s, %d file(s), commit %s", sp.logName, u.Username, plan.Node, len(res.Files), res.Commit)
	writeJSON(w, 200, out)
}

func stepSummary(steps []setupStepResult) map[string]string {
	m := map[string]string{}
	for _, st := range steps {
		m[st.ID] = st.Status
	}
	return m
}

// adminSecret reads the secret of manager.conf's [admin] user, "" when it is
// not there.
func adminSecret(w *nodecfg.World) string {
	txt, ok := w.Content(nodecfg.FileManager)
	if !ok {
		return ""
	}
	f, _ := astconf.Parse(nodecfg.FileManager, []byte(txt))
	for _, sec := range f.Section("admin") {
		if es := sec.Get("secret"); len(es) > 0 {
			return es[len(es)-1].Value
		}
	}
	return ""
}

func (s *Server) handleSetupRollback(w http.ResponseWriter, r *http.Request, u store.User) {
	svc := s.configSvc(w)
	if svc == nil {
		return
	}
	var in setupRollback
	if !decode(w, r, &in, false) {
		return
	}
	if !commitRe.MatchString(in.Commit) || !helper.ValidSetupNode(in.Node) || !helper.ValidSetupNode(in.OldNode) {
		writeErr(w, http.StatusBadRequest, "bad_request", "invalid rollback request")
		return
	}
	ctx := r.Context()
	steps := []setupStepResult{}
	step := func(id, status, detail string) {
		steps = append(steps, setupStepResult{ID: id, Status: status, Detail: detail})
	}
	fail := func(id string, err error) {
		step(id, "failed", err.Error())
		s.audit(u, "config.setup.rollback.failed", map[string]any{"from": in.Commit, "step": id})
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "rollback stopped at step " + id + ": " + err.Error(), "code": "apply_failed", "step": id, "steps": steps})
	}
	// files (savenode.conf is in the same history and comes back with them)
	res, err := svc.Restore(ctx, u.Username, in.Commit, !in.Restart)
	if err != nil {
		s.configError(w, err)
		return
	}
	step("files", "done", "")
	if in.AMI {
		if reload := s.amiLoginReloader(); reload != nil {
			if ok, err := reload(false); err != nil {
				fail("ami", err)
				return
			} else if ok {
				step("ami", "done", "")
			} else {
				step("ami", "skipped", "aslcp.conf sets the AMI secret itself")
			}
		}
	}
	if in.Restart {
		if err := s.restartAsteriskNow(ctx); err != nil {
			fail("restart", err)
			return
		}
		if err := s.waitAsteriskBack(ctx); err != nil {
			fail("restart", err)
			return
		}
		step("restart", "done", "")
	}
	if host := s.setupHost(); host != nil && (in.Node != in.OldNode || in.AMI) {
		req := helper.NodeSetupRequest{OldNode: in.Node, Node: in.OldNode}
		if in.AMI {
			if sec := adminSecret(s.cfgWorld(ctx, svc)); helper.ValidSetupSecret(sec) {
				req.AMIPass = sec
			}
		}
		ar, err := host.AllmonNodeSet(ctx, req)
		switch {
		case err != nil:
			step("allmon3", "failed", err.Error())
		case ar.Skipped != "":
			step("allmon3", "skipped", "Allmon3 is not installed")
		default:
			step("allmon3", "done", "")
		}
	}
	healthNodes := []string{in.OldNode}
	if in.Added {
		healthNodes = s.cfgWorld(ctx, svc).LocalNodes()
	}
	h := svc.HealthCheck(ctx, healthNodes)
	res.Health = &h
	s.rescanNodes()
	s.audit(u, "config.setup.rollback", map[string]any{"from": in.Commit, "files": res.Files, "commit": res.Commit, "steps": stepSummary(steps)})
	writeJSON(w, 200, map[string]any{"result": res, "steps": steps})
}
