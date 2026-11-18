package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/dvswitch"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// The private bridge node (docs/DVSWITCH.md, "Private node and USRP"): one
// endpoint plans or applies the whole change, one rolls it back.

const (
	dvRptFile     = "rpt.conf"
	dvModulesFile = "modules.conf"
	dvABFile      = "analog_bridge"
	dvABPath      = "Analog_Bridge.ini"
	dvBridgeLoop  = "127.0.0.1"
)

func (s *Server) dvPrivateRoutes() {
	m := s.mux
	m.HandleFunc("PUT /api/dvswitch/private-node", s.authed(store.RoleAdmin, s.handleDVPrivate))
	m.HandleFunc("POST /api/dvswitch/private-node/rollback", s.authed(store.RoleAdmin, s.handleDVPrivateRollback))
}

// dvPrivReq is the request of PUT /api/dvswitch/private-node.
type dvPrivReq struct {
	Node      string            `json:"node"`
	RxPort    int               `json:"rxPort"`
	TxPort    int               `json:"txPort"`
	LinkMode  string            `json:"linkMode"`
	LocalNode string            `json:"localNode"`
	DryRun    bool              `json:"dryRun"`
	Restart   bool              `json:"restart"`
	Base      map[string]string `json:"base"`
}

// DVPlanFile is one file of the plan.
type DVPlanFile struct {
	Path    string `json:"path"`
	Diff    string `json:"diff"`
	Changed bool   `json:"changed"`
}

// DVPlan is what an apply would do (and the reply of a dry run).
type DVPlan struct {
	Node            string                `json:"node"`
	RxPort          int                   `json:"rxPort"`
	TxPort          int                   `json:"txPort"`
	Layout          string                `json:"layout"`
	Files           []DVPlanFile          `json:"files"`
	RestartAsterisk bool                  `json:"restartAsterisk"`
	Restarts        []string              `json:"restarts"`
	Warnings        []string              `json:"warnings"`
	Base            map[string]string     `json:"base"`
	Link            dvswitch.LinkSettings `json:"link"`

	// what the apply writes
	files     []configsvc.File
	iniChange bool
	record    helper.DVPrivateRecord
	hadRecord *helper.DVPrivateRecord
}

// dvPrivErr is an error with its own status and code.
type dvPrivErr struct {
	status int
	code   string
	msg    string
}

func (e *dvPrivErr) Error() string { return e.msg }

func perr(status int, code, f string, a ...any) error {
	return &dvPrivErr{status, code, fmt.Sprintf(f, a...)}
}

func (s *Server) dvPrivateError(w http.ResponseWriter, err error) {
	var pe *dvPrivErr
	switch {
	case errors.As(err, &pe):
		writeErr(w, pe.status, pe.code, pe.msg)
	case isConfigErr(err):
		s.configError(w, err)
	default:
		s.dvswitchError(w, err)
	}
}

func isConfigErr(err error) bool {
	var (
		ce *helper.ConflictError
		se *configsvc.SyntaxErrors
		ie *configsvc.InvalidError
		ne *configsvc.NotFoundError
	)
	return errors.As(err, &ce) || errors.As(err, &se) || errors.As(err, &ie) || errors.As(err, &ne)
}

// dvPrivateUndo is what the last apply changed, kept for the rollback.
type dvPrivateUndo struct {
	mu        sync.Mutex
	have      bool
	before    string // config commit before the apply ("" when rpt/modules did not change)
	iniDone   bool
	hadRecord *helper.DVPrivateRecord
	prev, cur dvswitch.LinkSettings
	restarted bool
	at        time.Time
}

// dvPrivatePlan validates the request and computes the plan from fresh reads.
func (s *Server) dvPrivatePlan(ctx context.Context, in dvPrivReq) (*DVPlan, error) {
	if s.d.DVSwitch == nil {
		return nil, errDVUnavailable
	}
	svc := s.d.ConfigEditor
	if svc == nil {
		return nil, perr(http.StatusServiceUnavailable, "config_unavailable", "configuration editing is not available")
	}
	n, err := dvswitch.ValidNode(in.Node)
	if err != nil {
		return nil, perr(http.StatusBadRequest, "invalid_node", "%v", err)
	}
	node := strconv.Itoa(n)
	switch in.LinkMode {
	case "":
		in.LinkMode = dvswitch.LinkOnDemand
	case dvswitch.LinkOnDemand, dvswitch.LinkPermanent:
	default:
		return nil, perr(http.StatusBadRequest, "bad_request", `linkMode must be "ondemand" or "permanent"`)
	}
	if in.RxPort == 0 {
		in.RxPort = dvswitch.DefaultRxPort
	}
	if in.TxPort == 0 {
		in.TxPort = dvswitch.DefaultTxPort
	}
	if in.LocalNode != "" {
		if !isAllDigits(in.LocalNode) {
			return nil, perr(http.StatusBadRequest, "bad_request", "invalid local node")
		}
		if in.LocalNode == node || !s.isLocalNode(in.LocalNode) {
			return nil, perr(http.StatusBadRequest, "bad_request", "the local node must be one of this server's nodes, not the bridge node")
		}
	} else if in.LinkMode == dvswitch.LinkPermanent {
		return nil, perr(http.StatusBadRequest, "bad_request", "a permanent link needs a local node")
	}

	setup, err := s.d.DVSwitch.DVSwitchSetup(ctx)
	if err != nil {
		return nil, err
	}
	rptC, err := svc.Read(ctx, dvRptFile)
	if err != nil {
		return nil, err
	}
	tree, err := svc.Tree(ctx, dvRptFile)
	if err != nil {
		return nil, err
	}

	// node number: free, or already the bridge node
	owned := ""
	if setup.Private != nil {
		owned = setup.Private.Node
	}
	detected, _ := s.dvDetectedBridge(ctx)
	if other := firstNonEmpty(owned, detected); other != "" && other != node {
		return nil, perr(http.StatusConflict, "node_in_use", "bridge node %s already exists: change its ports or link instead, or uninstall DVSwitch first", other)
	}
	used := dvswitch.UsedNodes(tree)
	if used[node] && node != owned && node != detected {
		return nil, perr(http.StatusConflict, "node_in_use", "node %s is already in rpt.conf", node)
	}
	if used[node] && node == detected && node != owned && !dvswitch.IsUSRPNode(tree, node) {
		return nil, perr(http.StatusConflict, "node_in_use", "node %s is already in rpt.conf", node)
	}
	// ports
	if err := dvswitch.CheckPorts(in.RxPort, in.TxPort, setup.ReservedPorts, dvswitch.USRPPorts(tree, node)); err != nil {
		var pc *dvswitch.PortConflict
		if errors.As(err, &pc) {
			return nil, perr(http.StatusConflict, "port_conflict", "%v", err)
		}
		return nil, perr(http.StatusBadRequest, "invalid_field", "%v", err)
	}

	plan := &DVPlan{Node: node, RxPort: in.RxPort, TxPort: in.TxPort, Files: []DVPlanFile{}, Restarts: []string{}, Warnings: []string{},
		Base: map[string]string{}, Link: dvswitch.LinkSettings{Mode: in.LinkMode, LocalNode: in.LocalNode, Bridge: node}, hadRecord: setup.Private}
	if w := dvswitch.NodeWarning(node); w != "" {
		plan.Warnings = append(plan.Warnings, w)
	}

	// rpt.conf
	local := in.LocalNode
	if local == "" {
		local = s.dvLocalFor(dvswitch.LinkSettings{Mode: dvswitch.LinkOnDemand}, node)
	}
	res, err := astconf.AddBridgeNode([]byte(rptC.Content), astconf.BridgeSpec{Node: node, RxPort: in.RxPort, TxPort: in.TxPort, Local: local})
	if err != nil {
		return nil, perr(http.StatusUnprocessableEntity, "syntax_error", "rpt.conf: %v", err)
	}
	plan.Layout = res.Layout
	switch res.Layout {
	case astconf.LayoutMinimal:
		plan.Warnings = append(plan.Warnings, "No local node to inherit from: the new node gets a minimal stanza of its own.")
	case astconf.LayoutFlat:
		plan.Warnings = append(plan.Warnings, "The local node has no template: the new node copies its function, telemetry and timer stanza names.")
	}
	plan.Base[dvRptFile] = rptC.SHA256
	rptNew := string(res.Out)
	var modNew string
	modC, mErr := svc.Read(ctx, dvModulesFile)
	var modWas string
	switch {
	case mErr == nil:
		out, was, err := astconf.SetModuleLoad([]byte(modC.Content), astconf.ModuleUSRP)
		if err != nil {
			return nil, perr(http.StatusUnprocessableEntity, "syntax_error", "modules.conf: %v", err)
		}
		modNew, modWas = string(out), was
		plan.Base[dvModulesFile] = modC.SHA256
	default:
		var ne *configsvc.NotFoundError
		if !errors.As(mErr, &ne) {
			return nil, mErr
		}
		plan.Warnings = append(plan.Warnings, "modules.conf does not exist: chan_usrp is left to Asterisk's autoload.")
	}

	// the record: what ASLCP changed
	rec := helper.DVPrivateRecord{Version: 1, Node: node, StanzaAdded: res.StanzaAdded, StanzaText: res.StanzaText, BlankAdded: res.BlankAdded,
		NodesLineAdded: res.NodesLineAdded, NodesLine: res.NodesLine, ChanUsrpWas: modWas, LinkMode: in.LinkMode, LocalNode: in.LocalNode}
	if old := setup.Private; old != nil {
		rec.BlankAdded, rec.ChanUsrpWas = old.BlankAdded, old.ChanUsrpWas
		rec.StanzaAdded, rec.NodesLineAdded, rec.NodesLine = old.StanzaAdded, old.NodesLineAdded, old.NodesLine
		rec.StanzaText = old.StanzaText
		// the recorded stanza follows a port change only while it is still
		// exactly what ASLCP wrote; a stanza edited by hand stays refused
		if old.StanzaAdded {
			if _, _, _, probs := astconf.RemoveBridgeNode([]byte(rptC.Content), old.Node, old.StanzaText, "", old.BlankAdded); len(probs) == 0 {
				rec.StanzaText = res.StanzaText
			}
		}
		if rec.ChanUsrpWas == "" {
			rec.ChanUsrpWas = modWas
		}
	}
	if rec.ChanUsrpWas == "" {
		rec.ChanUsrpWas = astconf.ModAbsent
		if mErr != nil {
			rec.ChanUsrpWas = astconf.ModLoad // nothing to revert
		}
	}
	plan.record = rec

	addFile := func(path, cur, next, base string) {
		pf := DVPlanFile{Path: path}
		if cur != next {
			pf.Changed = true
			pf.Diff = configsvc.UnifiedDiff(path, cur, next, false)
			plan.files = append(plan.files, configsvc.File{Path: path, Content: next, Base: base})
		}
		plan.Files = append(plan.Files, pf)
	}
	addFile(dvRptFile, rptC.Content, rptNew, rptC.SHA256)
	if mErr == nil {
		addFile(dvModulesFile, modC.Content, modNew, modC.SHA256)
	}
	if len(plan.files) > 0 {
		pv, err := svc.Preview(ctx, plan.files)
		if err != nil {
			return nil, err
		}
		for _, f := range pv.Files {
			if f.Error != nil {
				return nil, &configsvc.SyntaxErrors{Issues: []configsvc.SyntaxIssue{{Path: f.Path, Line: f.Error.Line, Message: f.Error.Message}}}
			}
		}
		// every change of rpt.conf or modules.conf is a restart: a new node
		// needs one and the stanza is not applied by a reload
		plan.RestartAsterisk = true
	}

	// Analog_Bridge.ini [USRP]
	cur := setup.USRP
	iniDiff := ""
	var lines []string
	chg := func(key, old, nw string) {
		if old != nw {
			lines = append(lines, "-"+key+" = "+old, "+"+key+" = "+nw)
		}
	}
	chg("address", cur.Address, dvBridgeLoop)
	chg("rxPort", strconv.Itoa(cur.RxPort), strconv.Itoa(in.RxPort))
	chg("txPort", strconv.Itoa(cur.TxPort), strconv.Itoa(in.TxPort))
	if len(lines) > 0 {
		plan.iniChange = true
		iniDiff = "--- a/" + dvABPath + "\n+++ b/" + dvABPath + "\n@@ [USRP] @@\n" + strings.Join(lines, "\n") + "\n"
	}
	plan.Files = append(plan.Files, DVPlanFile{Path: dvABPath, Diff: iniDiff, Changed: plan.iniChange})
	for _, f := range setup.Files {
		if f.ID == dvABFile {
			plan.Base[dvABFile] = f.SHA256
		}
	}
	if plan.iniChange || plan.RestartAsterisk {
		plan.Restarts = []string{"analog_bridge", "mmdvm_bridge"}
	}
	sort.Strings(plan.Warnings)
	return plan, nil
}

func firstNonEmpty(a ...string) string {
	for _, x := range a {
		if x != "" {
			return x
		}
	}
	return ""
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// dvDetectedBridge is the node rpt.conf already gives a USRP channel.
func (s *Server) dvDetectedBridge(ctx context.Context) (string, bool) {
	if s.d.DVBridgeNode == nil {
		return "", false
	}
	return s.d.DVBridgeNode(ctx)
}

func (s *Server) handleDVPrivate(w http.ResponseWriter, r *http.Request, u store.User) {
	var in dvPrivReq
	if !decode(w, r, &in, false) {
		return
	}
	ctx := r.Context()
	plan, err := s.dvPrivatePlan(ctx, in)
	if err != nil {
		s.dvPrivateError(w, err)
		return
	}
	if in.DryRun {
		writeJSON(w, 200, plan)
		return
	}
	if plan.RestartAsterisk && !in.Restart {
		writeErr(w, http.StatusConflict, "restart_required", "this change needs an Asterisk restart: send restart: true to confirm")
		return
	}
	for k, b := range in.Base {
		if b != "" && plan.Base[k] != "" && b != plan.Base[k] {
			writeJSON(w, http.StatusConflict, map[string]any{"error": "a file changed on disk since the plan was made: plan again", "code": "conflict", "files": []string{k}})
			return
		}
	}
	s.dvPrivateApply(w, r, u, in, plan)
}

type dvStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// dvPrivateApply runs the plan: Asterisk config, then the ini, then the
// record, the Asterisk restart, the bridge restart and the health check.
func (s *Server) dvPrivateApply(w http.ResponseWriter, r *http.Request, u store.User, in dvPrivReq, plan *DVPlan) {
	ctx := r.Context()
	svc := s.d.ConfigEditor
	undo := &dvPrivateUndo{hadRecord: plan.hadRecord, prev: s.dvLinkSettings(), cur: plan.Link, at: time.Now()}
	rollbackAll := func() {
		s.dvPrivateRevertState(ctx, u, undo)
	}
	// 1. rpt.conf and modules.conf (no reload: they need a restart)
	if len(plan.files) > 0 {
		res, err := svc.Apply(ctx, u.Username, plan.files, "DVSwitch: bridge node "+plan.Node, false)
		if err != nil {
			s.audit(u, "dvswitch.private.refused", map[string]any{"node": plan.Node, "step": "asterisk_config"})
			s.dvPrivateError(w, err)
			return
		}
		undo.before = res.Before
	}
	// 2. Analog_Bridge.ini
	if plan.iniChange {
		_, err := s.d.DVSwitch.DVSwitchUSRP(ctx, helper.DVSwitchRequest{Author: u.Username, Base: map[string]string{dvABFile: plan.Base[dvABFile]},
			USRP: &helper.DVUSRP{Address: dvBridgeLoop, RxPort: plan.RxPort, TxPort: plan.TxPort}})
		if err != nil {
			rollbackAll()
			s.audit(u, "dvswitch.private.refused", map[string]any{"node": plan.Node, "step": "ini"})
			s.dvPrivateError(w, err)
			return
		}
		undo.iniDone = true
	}
	// 3. the record of what was changed, and the link settings
	rec := plan.record
	if _, err := s.d.DVSwitch.DVSwitchPrivate(ctx, helper.DVPrivate{Action: "set", Record: &rec}); err != nil {
		rollbackAll()
		s.dvPrivateError(w, err)
		return
	}
	if s.d.Store != nil {
		if err := dvswitch.WriteLinkSettings(s.d.Store, plan.Link); err != nil {
			rollbackAll()
			s.internal(w, err)
			return
		}
	}
	s.dvPrivSetUndo(undo)
	s.dvs.invalidate()
	steps := []dvStep{{Name: "asterisk_config", OK: true}, {Name: "ini", OK: true}}
	fail := func(step, msg string, health any) {
		s.audit(u, "dvswitch.private.failed", map[string]any{"node": plan.Node, "step": step})
		s.d.Logger.Printf("dvswitch private node %s: %s failed: %s", plan.Node, step, msg)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "apply_failed", "step": step, "applied": true,
			"steps": steps, "health": health, "rollback": s.dvPrivRollbackInfo()})
	}
	// 4. Asterisk
	if plan.RestartAsterisk {
		if err := s.restartAsteriskNow(ctx); err != nil {
			steps = append(steps, dvStep{Name: "asterisk_restart", Detail: err.Error()})
			fail("asterisk_restart", "Asterisk did not restart: "+err.Error(), nil)
			return
		}
		undo.restarted = true
		steps = append(steps, dvStep{Name: "asterisk_restart", OK: true})
	}
	// 5. the bridge
	var states []helper.DVSwitchUnit
	if len(plan.Restarts) > 0 {
		rr, err := s.d.DVSwitch.DVSwitchRestart(ctx, helper.DVSwitchRequest{Scope: "bridge", Action: "restart", Author: u.Username})
		if err != nil {
			steps = append(steps, dvStep{Name: "bridge_restart", Detail: err.Error()})
			fail("bridge_restart", "the DVSwitch bridge did not restart: "+err.Error(), nil)
			return
		}
		states = rr.States
		steps = append(steps, dvStep{Name: "bridge_restart", OK: true})
	}
	// 6. health
	h := s.dvPrivHealth(ctx, plan.Node, states)
	if !h.OK {
		steps = append(steps, dvStep{Name: "health", Detail: "a check failed"})
		fail("health", "the health check failed after the change", h)
		return
	}
	steps = append(steps, dvStep{Name: "health", OK: true})
	if s.d.DVLink != nil {
		s.d.DVLink.Saved(ctx, undo.prev, plan.Link)
	}
	s.audit(u, "dvswitch.private", map[string]any{"node": plan.Node, "layout": plan.Layout, "link": plan.Link.Mode, "restart": plan.RestartAsterisk, "ini": plan.iniChange})
	s.d.Logger.Printf("dvswitch private node %s applied by %s (%s)", plan.Node, u.Username, plan.Layout)
	s.dvs.invalidate()
	s.dvs.afterCommand()
	writeJSON(w, 200, map[string]any{"applied": true, "node": plan.Node, "steps": steps, "health": h, "rollback": s.dvPrivRollbackInfo()})
}

func (s *Server) dvPrivHealth(ctx context.Context, node string, states []helper.DVSwitchUnit) configsvc.Health {
	nodes := []string{node}
	if m := s.monitor(); m != nil {
		nodes = append(nodes, m.Nodes()...)
	}
	h := configsvc.Health{OK: true, Checks: []configsvc.Check{}}
	if svc := s.d.ConfigEditor; svc != nil {
		h = svc.HealthCheck(ctx, dedupe(nodes))
	}
	for _, st := range states {
		if st.Name != "analog_bridge" && st.Name != "mmdvm_bridge" {
			continue
		}
		ok := st.Active == "active"
		h.Checks = append(h.Checks, configsvc.Check{Name: "unit " + st.Name, OK: ok, Detail: st.Active})
		if !ok {
			h.OK = false
		}
	}
	return h
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range in {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// ---- rollback

func (s *Server) dvPrivSetUndo(u *dvPrivateUndo) {
	u.have = true
	s.dvUndoMu.Lock()
	s.dvUndo = u
	s.dvUndoMu.Unlock()
}

func (s *Server) dvPrivRollbackInfo() map[string]any {
	s.dvUndoMu.Lock()
	defer s.dvUndoMu.Unlock()
	u := s.dvUndo
	if u == nil || !u.have {
		return map[string]any{"available": false}
	}
	return map[string]any{"available": true, "before": u.before, "ini": u.iniDone}
}

// dvPrivateRevertState undoes the steps the apply did: the Asterisk files
// (their commit before), the ini (its newest history copy) and the record.
func (s *Server) dvPrivateRevertState(ctx context.Context, u store.User, undo *dvPrivateUndo) []string {
	var problems []string
	if undo.before != "" && s.d.ConfigEditor != nil {
		if _, err := s.d.ConfigEditor.Restore(ctx, u.Username, undo.before, false); err != nil {
			problems = append(problems, "Asterisk files: "+err.Error())
		}
	}
	if undo.iniDone {
		if _, err := s.d.DVSwitch.DVSwitchRollback(ctx, helper.DVSwitchRequest{File: dvABFile, Author: u.Username}); err != nil {
			problems = append(problems, "Analog_Bridge.ini: "+err.Error())
		}
	}
	act := helper.DVPrivate{Action: "clear"}
	if undo.hadRecord != nil {
		act = helper.DVPrivate{Action: "set", Record: undo.hadRecord}
	}
	if _, err := s.d.DVSwitch.DVSwitchPrivate(ctx, act); err != nil {
		problems = append(problems, "record: "+err.Error())
	}
	if s.d.Store != nil {
		_ = dvswitch.WriteLinkSettings(s.d.Store, undo.prev)
	}
	return problems
}

func (s *Server) handleDVPrivateRollback(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVSwitch == nil {
		s.dvswitchError(w, errDVUnavailable)
		return
	}
	s.dvUndoMu.Lock()
	undo := s.dvUndo
	s.dvUndoMu.Unlock()
	if undo == nil || !undo.have {
		writeErr(w, http.StatusNotFound, "no_rollback", "there is no change to roll back")
		return
	}
	ctx := r.Context()
	problems := s.dvPrivateRevertState(ctx, u, undo)
	var steps []dvStep
	if undo.restarted {
		if err := s.restartAsteriskNow(ctx); err != nil {
			steps = append(steps, dvStep{Name: "asterisk_restart", Detail: err.Error()})
		} else {
			steps = append(steps, dvStep{Name: "asterisk_restart", OK: true})
		}
	}
	if undo.restarted || undo.iniDone {
		if _, err := s.d.DVSwitch.DVSwitchRestart(ctx, helper.DVSwitchRequest{Scope: "bridge", Action: "restart", Author: u.Username}); err != nil {
			steps = append(steps, dvStep{Name: "bridge_restart", Detail: err.Error()})
		} else {
			steps = append(steps, dvStep{Name: "bridge_restart", OK: true})
		}
	}
	if s.d.DVLink != nil {
		s.d.DVLink.Saved(ctx, undo.cur, undo.prev)
	}
	if len(problems) == 0 {
		s.dvUndoMu.Lock()
		s.dvUndo = nil
		s.dvUndoMu.Unlock()
	}
	s.audit(u, "dvswitch.private.rollback", map[string]any{"problems": len(problems)})
	s.d.Logger.Printf("dvswitch private node rolled back by %s (%d problems)", u.Username, len(problems))
	s.dvs.invalidate()
	s.dvs.afterCommand()
	if problems == nil {
		problems = []string{}
	}
	writeJSON(w, 200, map[string]any{"rolledBack": len(problems) == 0, "problems": problems, "steps": steps})
}

// DVBridgeSetup is the data of the Setup section "Bridge node": the node
// ASLCP suggests, the numbers that are taken, the local nodes the link can use
// and the prefilled ports.
type DVBridgeSetup struct {
	// Node is the existing bridge node ("" when none yet); Owned says that ASLCP
	// added it (so the uninstall removes it).
	Node  string `json:"node"`
	Owned bool   `json:"owned"`
	// Suggested is the first free number of 1900-1997 ("" when none is left).
	Suggested  string                `json:"suggested"`
	Used       []string              `json:"used"`
	LocalNodes []string              `json:"localNodes"`
	RxPort     int                   `json:"rxPort"`
	TxPort     int                   `json:"txPort"`
	Link       dvswitch.LinkSettings `json:"link"`
	Paused     bool                  `json:"paused"`
}

func (s *Server) dvBridgeSetup(ctx context.Context, v helper.DVSwitchSetup) DVBridgeSetup {
	b := DVBridgeSetup{Used: []string{}, LocalNodes: []string{}, RxPort: dvswitch.DefaultRxPort, TxPort: dvswitch.DefaultTxPort}
	if s.d.ConfigEditor != nil {
		if t, err := s.d.ConfigEditor.Tree(ctx, dvRptFile); err == nil {
			used := dvswitch.UsedNodes(t)
			for n := range used {
				b.Used = append(b.Used, n)
			}
			sort.Strings(b.Used)
			b.Suggested = dvswitch.SuggestNode(used)
		}
	}
	if m := s.monitor(); m != nil {
		b.LocalNodes = append(b.LocalNodes, m.Nodes()...)
		sort.Strings(b.LocalNodes)
	}
	if v.Private != nil {
		b.Node, b.Owned = v.Private.Node, true
	} else if n, ok := s.dvDetectedBridge(ctx); ok {
		b.Node = n
	}
	if b.Node != "" && v.USRP.RxPort != 0 && v.USRP.TxPort != 0 && v.USRP.RxPort != v.USRP.TxPort {
		b.RxPort, b.TxPort = v.USRP.RxPort, v.USRP.TxPort
	}
	b.Link = s.dvLinkSettings()
	b.Link.Bridge = b.Node
	if b.Link.LocalNode == "" {
		b.Link.LocalNode = s.dvLocalFor(dvswitch.LinkSettings{Mode: dvswitch.LinkOnDemand}, b.Node)
	}
	if s.d.Store != nil {
		p, _ := s.d.Store.GetSetting(dvswitch.SettingLinkPaused)
		b.Paused = p == "1"
	}
	return b
}
