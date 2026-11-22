package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/astconf"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/dvpanel"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// The private bridge node (docs/DV.md, "Private node and USRP"): one
// endpoint plans or applies the whole change, one rolls it back.

const (
	dvRptFile     = "rpt.conf"
	dvModulesFile = "modules.conf"
)

func (s *Server) dvPrivateRoutes() {
	m := s.mux
	m.HandleFunc("PUT /api/dv/private-node", s.authed(store.RoleAdmin, s.handleDVPrivate))
	// "Disconnect other links first" is an operator's panel setting: a settings
	// write, no config apply and no restart.
	m.HandleFunc("PUT /api/dv/link", s.authed(store.RoleOperator, s.handleDVLinkExclusive))
	m.HandleFunc("PUT /api/dv/private-node/name", s.authed(store.RoleAdmin, s.handleDVNodeName))
	m.HandleFunc("DELETE /api/dv/private-node", s.authed(store.RoleAdmin, s.handleDVPrivateRemove))
	m.HandleFunc("POST /api/dv/private-node/rollback", s.authed(store.RoleAdmin, s.handleDVPrivateRollback))
	s.dvAdoptRoutes()
}

// dvNodeName is the display name of the bridge node: the saved one or the default.
func (s *Server) dvNodeName() string {
	if s.d.Store != nil {
		if v, err := s.d.Store.GetSetting(dvpanel.SettingNodeName); err == nil {
			if n, ok := dvNodeNameValid(v); ok && n != "" {
				return n
			}
		}
	}
	return dvpanel.DefaultNodeName
}

// dvNodeNameValid trims a name and checks it: at most 40 printable
// characters; empty is allowed (it means the default).
func dvNodeNameValid(v string) (string, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", true
	}
	return dvFavName(v)
}

// handleDVNodeName: PUT /api/dv/private-node/name {"name": "..."} saves
// the display name of the bridge node (dv.node_name alone: no config
// apply, no restart). An empty name restores the default.
func (s *Server) handleDVNodeName(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Name *string `json:"name"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	if in.Name == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "name is required")
		return
	}
	name, ok := dvNodeNameValid(*in.Name)
	if !ok {
		writeErr(w, http.StatusBadRequest, "bad_request", "the name must be 1 to 40 printable characters, or empty for the default")
		return
	}
	if s.d.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "settings are not available")
		return
	}
	if err := s.d.Store.SetSetting(dvpanel.SettingNodeName, name); err != nil {
		s.internal(w, err)
		return
	}
	s.audit(u, "dv.node_name", map[string]any{"name": name})
	s.dvs.refreshSoon()
	n := name
	if n == "" {
		n = dvpanel.DefaultNodeName
	}
	writeJSON(w, 200, map[string]any{"name": n})
}

// handleDVLinkExclusive: PUT /api/dv/link {"exclusive": bool} saves
// dv.link_exclusive alone.
func (s *Server) handleDVLinkExclusive(w http.ResponseWriter, r *http.Request, u store.User) {
	var in struct {
		Exclusive *bool `json:"exclusive"`
	}
	if !decode(w, r, &in, false) {
		return
	}
	if in.Exclusive == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "exclusive is required")
		return
	}
	if s.d.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "unavailable", "settings are not available")
		return
	}
	val := "0"
	if *in.Exclusive {
		val = "1"
	}
	if err := s.d.Store.SetSetting(dvpanel.SettingLinkExclusive, val); err != nil {
		s.internal(w, err)
		return
	}
	if s.d.DVLink != nil {
		s.d.DVLink.ExclusiveChanged(*in.Exclusive)
	}
	s.audit(u, "dv.link", map[string]any{"exclusive": *in.Exclusive})
	s.dvs.invalidate()
	s.dvLinkChanged()
	writeJSON(w, 200, map[string]any{"exclusive": *in.Exclusive})
}

// dvPrivReq is the request of PUT /api/dv/private-node.
type dvPrivReq struct {
	Node      string `json:"node"`
	RxPort    int    `json:"rxPort"`
	TxPort    int    `json:"txPort"`
	LinkMode  string `json:"linkMode"`
	LocalNode string `json:"localNode"`
	// LinkExclusive is "Disconnect other links first". Absent keeps the
	// saved value (the default when none was saved).
	LinkExclusive *bool             `json:"linkExclusive"`
	DryRun        bool              `json:"dryRun"`
	Restart       bool              `json:"restart"`
	Base          map[string]string `json:"base"`
	// Renumber says that Node replaces the bridge node ASLCP added (the old
	// stanza and [nodes] line are removed, the new ones added, one restart).
	// Without it a different number is node_in_use, so a stale form or a
	// typo cannot move the node.
	Renumber bool `json:"renumber"`
}

// Plan kinds.
const (
	dvKindSet      = "set"      // add or update the bridge node
	dvKindRenumber = "renumber" // replace the bridge node by another number
	dvKindRemove   = "remove"   // take the bridge node out
)

// DVPlanFile is one file of the plan.
type DVPlanFile struct {
	Path    string `json:"path"`
	Diff    string `json:"diff"`
	Changed bool   `json:"changed"`
}

// DVPlan is what an apply would do (and the reply of a dry run).
type DVPlan struct {
	// Kind is "set", "renumber" or "remove". Node is the node the plan ends
	// with (for a removal, the node that is removed); OldNode is the number
	// a renumber replaces. Unlinks are the local nodes that are disconnected
	// from the old or removed bridge node first. KeepRecord (removal) says
	// that something could not be reverted, so the record of what ASLCP
	// changed stays.
	Kind            string               `json:"kind"`
	OldNode         string               `json:"oldNode,omitempty"`
	Unlinks         []string             `json:"unlinks"`
	KeepRecord      bool                 `json:"keepRecord,omitempty"`
	Node            string               `json:"node"`
	RxPort          int                  `json:"rxPort"`
	TxPort          int                  `json:"txPort"`
	Layout          string               `json:"layout"`
	Files           []DVPlanFile         `json:"files"`
	RestartAsterisk bool                 `json:"restartAsterisk"`
	Restarts        []string             `json:"restarts"`
	Warnings        []string             `json:"warnings"`
	Base            map[string]string    `json:"base"`
	Link            dvpanel.LinkSettings `json:"link"`

	// what the apply writes
	files     []configsvc.File
	record    helper.DVPrivateRecord
	hadRecord *helper.DVPrivateRecord
	oldNode   string // the node whose link is disconnected first
}

// addFile puts a file into the plan, with its diff when it changes.
func (p *DVPlan) addFile(path, cur, next, base string) {
	pf := DVPlanFile{Path: path}
	if cur != next {
		pf.Changed = true
		pf.Diff = configsvc.UnifiedDiff(path, cur, next, false)
		p.files = append(p.files, configsvc.File{Path: path, Content: next, Base: base})
	}
	p.Files = append(p.Files, pf)
}

// previewFiles syntax-checks the changed Asterisk files; any change of
// rpt.conf or modules.conf is a restart (neither is reloadable here).
func (p *DVPlan) previewFiles(ctx context.Context, svc *configsvc.Service) error {
	if len(p.files) == 0 {
		return nil
	}
	pv, err := svc.Preview(ctx, p.files)
	if err != nil {
		return err
	}
	for _, f := range pv.Files {
		if f.Error != nil {
			return &configsvc.SyntaxErrors{Issues: []configsvc.SyntaxIssue{{Path: f.Path, Line: f.Error.Line, Message: f.Error.Message}}}
		}
	}
	p.RestartAsterisk = true
	return nil
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
		s.dvError(w, err)
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
	cfgDone   bool   // the engine config was rendered
	hadRecord *helper.DVPrivateRecord
	prev, cur dvpanel.LinkSettings
	restarted bool
	at        time.Time
}

// dvPrivatePlan validates the request and computes the plan from fresh reads.
func (s *Server) dvPrivatePlan(ctx context.Context, in dvPrivReq) (*DVPlan, error) {
	if s.d.DVEngine == nil {
		return nil, errDVUnavailable
	}
	svc := s.d.ConfigEditor
	if svc == nil {
		return nil, perr(http.StatusServiceUnavailable, "config_unavailable", "configuration editing is not available")
	}
	n, err := dvpanel.ValidNode(in.Node)
	if err != nil {
		return nil, perr(http.StatusBadRequest, "invalid_node", "%v", err)
	}
	node := strconv.Itoa(n)
	setup, err := s.dvSetupState(ctx)
	if err != nil {
		return nil, err
	}
	if in.Renumber && setup.Private != nil {
		// a renumber changes the number and nothing else unless asked: the
		// ports and the link settings carry over
		if cur := s.dvLinkSettings(); in.LinkMode == "" {
			in.LinkMode = cur.Mode
			if in.LocalNode == "" {
				in.LocalNode = cur.LocalNode
			}
		}
		if in.RxPort == 0 && in.TxPort == 0 {
			in.RxPort, in.TxPort = setup.USRP.RxPort, setup.USRP.TxPort
		}
	}
	linkExclusive := s.dvLinkSettings().Exclusive
	if in.LinkExclusive != nil {
		linkExclusive = *in.LinkExclusive
	}
	switch in.LinkMode {
	case "":
		in.LinkMode = dvpanel.LinkOnDemand
	case dvpanel.LinkOnDemand, dvpanel.LinkPermanent:
	default:
		return nil, perr(http.StatusBadRequest, "bad_request", `linkMode must be "ondemand" or "permanent"`)
	}
	if in.RxPort == 0 {
		in.RxPort = dvpanel.DefaultRxPort
	}
	if in.TxPort == 0 {
		in.TxPort = dvpanel.DefaultTxPort
	}
	if in.LocalNode != "" {
		if !isAllDigits(in.LocalNode) {
			return nil, perr(http.StatusBadRequest, "bad_request", "invalid local node")
		}
		if in.LocalNode == node || !s.isLocalNode(in.LocalNode) {
			return nil, perr(http.StatusBadRequest, "bad_request", "the local node must be one of this server's nodes, not the bridge node")
		}
	} else if in.LinkMode == dvpanel.LinkPermanent {
		return nil, perr(http.StatusBadRequest, "bad_request", "a permanent link needs a local node")
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
	kind, oldNode := dvKindSet, ""
	if other := firstNonEmpty(owned, detected); other != "" && other != node {
		switch {
		case !in.Renumber:
			return nil, perr(http.StatusConflict, "node_in_use", "bridge node %s already exists: change its ports or link instead, or use Change number to renumber it (Remove bridge node takes it out)", other)
		case owned == "":
			return nil, perr(http.StatusConflict, "node_in_use", "bridge node %s was not added by ASLCP and is left alone: it cannot be renumbered here", other)
		case !setup.Private.StanzaAdded:
			return nil, perr(http.StatusConflict, "old_node_not_removable", "the stanza [%s] existed before ASLCP and is not removed, so the node cannot be renumbered here: edit rpt.conf by hand", owned)
		}
		kind, oldNode = dvKindRenumber, owned
	} else if in.Renumber && owned == "" {
		return nil, perr(http.StatusConflict, "no_bridge_node", "there is no bridge node added by ASLCP to renumber")
	}
	used := dvpanel.UsedNodes(tree)
	if used[node] && node != owned && node != detected {
		return nil, perr(http.StatusConflict, "node_in_use", "node %s is already in rpt.conf", node)
	}
	if used[node] && node == detected && node != owned && !dvpanel.IsUSRPNode(tree, node) {
		return nil, perr(http.StatusConflict, "node_in_use", "node %s is already in rpt.conf", node)
	}
	// ports (the old bridge node of a renumber is going away: its ports are free)
	if err := dvpanel.CheckPorts(in.RxPort, in.TxPort, setup.ReservedPorts, dvpanel.USRPPorts(tree, node, oldNode)); err != nil {
		var pc *dvpanel.PortConflict
		if errors.As(err, &pc) {
			return nil, perr(http.StatusConflict, "port_conflict", "%v", err)
		}
		return nil, perr(http.StatusBadRequest, "invalid_field", "%v", err)
	}

	plan := &DVPlan{Kind: kind, OldNode: oldNode, Unlinks: []string{}, Node: node, RxPort: in.RxPort, TxPort: in.TxPort, Files: []DVPlanFile{}, Restarts: []string{}, Warnings: []string{},
		Base: map[string]string{}, Link: dvpanel.LinkSettings{Mode: in.LinkMode, LocalNode: in.LocalNode, Bridge: node, Exclusive: linkExclusive}, hadRecord: setup.Private, oldNode: oldNode}

	// rpt.conf
	local := in.LocalNode
	if local == "" {
		local = s.dvLocalFor(dvpanel.LinkSettings{Mode: dvpanel.LinkOnDemand}, node)
	}
	rptSrc := []byte(rptC.Content)
	if kind == dvKindRenumber {
		// the old stanza and [nodes] line go first, exactly as recorded: a
		// stanza edited by hand is refused instead of leaving two nodes
		old := setup.Private
		nodesLine := ""
		if old.NodesLineAdded {
			nodesLine = old.NodesLine
		}
		out, _, _, probs := astconf.RevertBridgeNode(rptSrc, old.Node, old.StanzaText, nodesLine, old.BlankAdded)
		for _, pr := range probs {
			if !pr.Gone {
				return nil, perr(http.StatusConflict, "old_node_edited", "bridge node %s cannot be renumbered: %s. Put it back as ASLCP wrote it or remove it by hand, then try again", old.Node, pr.Msg)
			}
			plan.Warnings = append(plan.Warnings, pr.Msg)
		}
		if !old.NodesLineAdded {
			plan.Warnings = append(plan.Warnings, fmt.Sprintf("The [nodes] line of %s was there before ASLCP and is left as it is.", old.Node))
		}
		rptSrc = out
		plan.Unlinks = s.dvBridgeLocals(old.Node)
	}
	res, err := astconf.AddBridgeNode(rptSrc, astconf.BridgeSpec{Node: node, RxPort: in.RxPort, TxPort: in.TxPort, Local: local})
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
		rec.ChanUsrpWas = old.ChanUsrpWas
		if kind == dvKindSet {
			rec.BlankAdded = old.BlankAdded
			rec.StanzaAdded, rec.NodesLineAdded, rec.NodesLine = old.StanzaAdded, old.NodesLineAdded, old.NodesLine
			rec.StanzaText = old.StanzaText
			// the recorded stanza follows a port change only while it is still
			// exactly what ASLCP wrote; a stanza edited by hand stays refused
			if old.StanzaAdded {
				if _, _, _, probs := astconf.RemoveBridgeNode([]byte(rptC.Content), old.Node, old.StanzaText, "", old.BlankAdded); len(probs) == 0 {
					rec.StanzaText = res.StanzaText
				}
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

	plan.addFile(dvRptFile, rptC.Content, rptNew, rptC.SHA256)
	if mErr == nil {
		plan.addFile(dvModulesFile, modC.Content, modNew, modC.SHA256)
	}
	if err := plan.previewFiles(ctx, svc); err != nil {
		return nil, err
	}

	// the engine owns its USRP ports (rendered from the record's stanza): the
	// record never carries ports to restore
	plan.record.UsrpUntouched = true
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
	plan, err := s.dvPrivatePlan(r.Context(), in)
	if err != nil {
		s.dvPrivateError(w, err)
		return
	}
	s.dvPrivateRun(w, r, u, in, plan)
}

// handleDVPrivateRemove: DELETE /api/dv/private-node. The body is
// optional and has the PUT's dryRun, restart and base; dryRun and restart are
// also read from the query (?dryRun=true&restart=true).
func (s *Server) handleDVPrivateRemove(w http.ResponseWriter, r *http.Request, u store.User) {
	var in dvPrivReq
	if !decode(w, r, &in, true) {
		return
	}
	q := r.URL.Query()
	in.DryRun = in.DryRun || q.Get("dryRun") == "true" || q.Get("dryRun") == "1"
	in.Restart = in.Restart || q.Get("restart") == "true" || q.Get("restart") == "1"
	plan, err := s.dvPrivateRemovePlan(r.Context(), in)
	if err != nil {
		if !in.DryRun {
			s.audit(u, "dv.private.remove.refused", map[string]any{"error": err.Error()})
		}
		s.dvPrivateError(w, err)
		return
	}
	s.dvPrivateRun(w, r, u, in, plan)
}

// dvPrivateRun answers a dry run with the plan, or checks the restart
// confirmation and the base hashes and applies.
func (s *Server) dvPrivateRun(w http.ResponseWriter, r *http.Request, u store.User, in dvPrivReq, plan *DVPlan) {
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

// dvPrivateRemovePlan plans the removal of the bridge node ASLCP added: the
// revert of rpt.conf and modules.conf from the record (the same pure function
// as the uninstall step), the ini's [USRP] ports back to what they were, and
// the link settings and record cleared. Items that cannot be reverted are
// warnings and keep the record.
func (s *Server) dvPrivateRemovePlan(ctx context.Context, in dvPrivReq) (*DVPlan, error) {
	if s.d.DVEngine == nil {
		return nil, errDVUnavailable
	}
	svc := s.d.ConfigEditor
	if svc == nil {
		return nil, perr(http.StatusServiceUnavailable, "config_unavailable", "configuration editing is not available")
	}
	setup, err := s.dvSetupState(ctx)
	if err != nil {
		return nil, err
	}
	rec := setup.Private
	if rec == nil {
		if n, _ := s.dvDetectedBridge(ctx); n != "" {
			return nil, perr(http.StatusConflict, "not_owned", "bridge node %s was not added by ASLCP and is left alone", n)
		}
		return nil, perr(http.StatusConflict, "no_bridge_node", "there is no bridge node added by ASLCP to remove")
	}
	rptC, err := svc.Read(ctx, dvRptFile)
	if err != nil {
		return nil, err
	}
	plan := &DVPlan{Kind: dvKindRemove, Unlinks: s.dvBridgeLocals(rec.Node), Node: rec.Node, Files: []DVPlanFile{}, Restarts: []string{}, Warnings: []string{},
		Base: map[string]string{dvRptFile: rptC.SHA256}, Link: dvpanel.LinkSettings{Mode: dvpanel.LinkOnDemand, Exclusive: s.dvLinkSettings().Exclusive}, hadRecord: rec, oldNode: rec.Node}
	plan.RxPort, plan.TxPort = setup.USRP.RxPort, setup.USRP.TxPort

	modC, mErr := svc.Read(ctx, dvModulesFile)
	var modPtr *string
	planRec := *rec
	switch {
	case mErr == nil:
		modPtr = &modC.Content
		plan.Base[dvModulesFile] = modC.SHA256
	default:
		var ne *configsvc.NotFoundError
		if !errors.As(mErr, &ne) {
			return nil, mErr
		}
		planRec.ChanUsrpWas = astconf.ModLoad // nothing to revert
		plan.Warnings = append(plan.Warnings, "modules.conf does not exist: nothing to restore there.")
	}
	rv := helper.PlanPrivateRemove(&planRec, &rptC.Content, modPtr)
	plan.Warnings = append(plan.Warnings, rv.Problems...)
	plan.Warnings = append(plan.Warnings, rv.Gone...)
	plan.KeepRecord = len(rv.Problems) > 0
	rptNew, modNew := rptC.Content, ""
	if rv.Rpt != nil {
		rptNew = *rv.Rpt
	}
	plan.addFile(dvRptFile, rptC.Content, rptNew, rptC.SHA256)
	if mErr == nil {
		modNew = modC.Content
		if rv.Modules != nil {
			modNew = *rv.Modules
		}
		plan.addFile(dvModulesFile, modC.Content, modNew, modC.SHA256)
	}
	if err := plan.previewFiles(ctx, svc); err != nil {
		return nil, err
	}

	// the record that stays when something could not be reverted: what was
	// reverted is struck from it, so a later removal only deals with the rest
	upd := *rec
	if rv.RemovedStanza {
		upd.StanzaAdded, upd.StanzaText, upd.BlankAdded = false, "", false
	}
	if rv.RemovedLine {
		upd.NodesLineAdded, upd.NodesLine = false, ""
	}
	if rv.RevertedModule {
		upd.ChanUsrpWas = astconf.ModLoad
	}
	upd.UsrpWas, upd.UsrpUntouched = nil, true
	plan.record = upd
	sort.Strings(plan.Warnings)
	return plan, nil
}

var reStanzaChannel = regexp.MustCompile(`(?mi)^\s*rxchannel\s*=\s*USRP/[^:\s]+:(\d+):(\d+)`)

// stanzaPorts reads the two ports out of a recorded stanza's rxchannel.
func stanzaPorts(stanza string) (rx, tx int, ok bool) {
	m := reStanzaChannel.FindStringSubmatch(stanza)
	if m == nil {
		return 0, 0, false
	}
	rx, _ = strconv.Atoi(m[1])
	tx, _ = strconv.Atoi(m[2])
	return rx, tx, true
}

// dvBridgeLocals lists the local nodes linked to the bridge node, plus the
// local node of a saved permanent link (which may be down right now but is
// remembered by Asterisk and would come back).
func (s *Server) dvBridgeLocals(bridge string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n != "" && n != bridge && !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if m := s.monitor(); m != nil {
		for _, n := range m.Nodes() {
			if linked, _ := (dvLinker{s}).Linked(n, bridge); linked {
				add(n)
			}
		}
	}
	if set := s.dvLinkSettings(); set.Mode == dvpanel.LinkPermanent && set.Bridge == bridge {
		add(set.LocalNode)
	}
	sort.Strings(out)
	if out == nil {
		out = []string{}
	}
	return out
}

// dvUnlinkBridge disconnects the bridge node from the local nodes first: a
// permanent link through the reconciler (the uninstall's call), any other link
// directly. Errors are returned for the step list; the apply goes on, because
// the restart drops an ordinary link anyway.
func (s *Server) dvUnlinkBridge(ctx context.Context, bridge string, locals []string) error {
	var errs []string
	if s.d.DVLink != nil {
		if err := s.d.DVLink.Unlink(ctx); err != nil {
			errs = append(errs, err.Error())
		}
	}
	set := s.dvLinkSettings()
	for _, n := range locals {
		if s.d.DVLink != nil && set.Mode == dvpanel.LinkPermanent && set.LocalNode == n && set.Bridge == bridge {
			continue // done by Unlink
		}
		if err := (dvLinker{s}).Disconnect(ctx, n, bridge, true); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

type dvStep struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// dvPrivateApply runs the plan: (renumber, remove) the disconnect of the old
// link, the Asterisk config, the ini, the record and the link settings, then
// the Asterisk restart, the bridge restart and the health check.
func (s *Server) dvPrivateApply(w http.ResponseWriter, r *http.Request, u store.User, in dvPrivReq, plan *DVPlan) {
	if s.d.DVLink != nil {
		defer s.d.DVLink.Hold()() // no automatic relink until this is done (Saved arms the exclusive)
	}
	ctx := r.Context()
	svc := s.d.ConfigEditor
	undo := &dvPrivateUndo{hadRecord: plan.hadRecord, prev: s.dvLinkSettings(), cur: plan.Link, at: time.Now()}
	unlinked := false
	rollbackAll := func() {
		s.dvPrivateRevertState(ctx, u, undo)
		if unlinked && s.d.DVLink != nil {
			s.d.DVLink.Saved(ctx, undo.cur, undo.prev) // the permanent link comes back
		}
	}
	steps := []dvStep{}
	// 0. a link to the old (renumber) or removed bridge node goes first: a
	// permanent one is remembered by Asterisk and would come back after the
	// restart. The link settings are cleared at once so that the reconciler
	// does not link it again meanwhile; a failure restores them.
	if plan.oldNode != "" {
		st := dvStep{Name: "disconnect", OK: true, Detail: "no link"}
		if len(plan.Unlinks) > 0 {
			st.Detail = "node " + strings.Join(plan.Unlinks, ", ")
		}
		if err := s.dvUnlinkBridge(ctx, plan.oldNode, plan.Unlinks); err != nil {
			st.OK, st.Detail = false, err.Error()
			s.d.Logger.Printf("dv private node %s: cannot disconnect the link: %v", plan.oldNode, err)
		}
		steps = append(steps, st)
		unlinked = true
		if s.d.Store != nil {
			if err := dvpanel.WriteLinkSettings(s.d.Store, dvpanel.LinkSettings{Mode: dvpanel.LinkOnDemand, Exclusive: undo.prev.Exclusive}); err != nil {
				rollbackAll()
				s.internal(w, err)
				return
			}
		}
	}
	refused := func(step string) {
		s.audit(u, "dv.private.refused", map[string]any{"node": plan.Node, "step": step, "kind": plan.Kind})
	}
	// 1. rpt.conf and modules.conf (no reload: they need a restart)
	if len(plan.files) > 0 {
		msg := "Digital modes: bridge node " + plan.Node
		switch plan.Kind {
		case dvKindRemove:
			msg = "Digital modes: remove bridge node " + plan.Node
		case dvKindRenumber:
			msg = "Digital modes: bridge node " + plan.OldNode + " to " + plan.Node
		}
		res, err := svc.Apply(ctx, u.Username, plan.files, msg, false)
		if err != nil {
			rollbackAll()
			refused("asterisk_config")
			s.dvPrivateError(w, err)
			return
		}
		undo.before = res.Before
	}
	steps = append(steps, dvStep{Name: "asterisk_config", OK: true})
	// 2. the record of what was changed (a removal clears it unless
	// something stayed), and the link settings
	var recErr error
	if plan.Kind == dvKindRemove && !plan.KeepRecord {
		_, recErr = s.d.DVEngine.DVPrivateClear(ctx)
	} else {
		_, recErr = s.d.DVEngine.DVPrivateSet(ctx, plan.record)
	}
	if recErr != nil {
		rollbackAll()
		s.dvPrivateError(w, recErr)
		return
	}
	if s.d.Store != nil {
		if err := dvpanel.WriteLinkSettings(s.d.Store, plan.Link); err != nil {
			rollbackAll()
			s.internal(w, err)
			return
		}
	}
	s.dvPrivSetUndo(undo)
	s.dvs.invalidate()
	fail := func(step, msg string, health any) {
		s.audit(u, "dv.private.failed", map[string]any{"node": plan.Node, "step": step, "kind": plan.Kind})
		s.d.Logger.Printf("dv private node %s: %s failed: %s", plan.Node, step, msg)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": msg, "code": "apply_failed", "step": step, "applied": true,
			"steps": steps, "health": health, "rollback": s.dvPrivRollbackInfo()})
	}
	// 3. the engine's config, rendered from the record
	if s.dvEnabled() {
		if err := s.dvRenderConfig(ctx); err != nil {
			steps = append(steps, dvStep{Name: "engine_config", Detail: err.Error()})
			fail("engine_config", "the engine config was not written: "+err.Error(), nil)
			return
		}
		undo.cfgDone = true
		steps = append(steps, dvStep{Name: "engine_config", OK: true})
	}
	// 4. Asterisk
	if plan.RestartAsterisk {
		if err := s.restartAsteriskNow(ctx); err != nil {
			steps = append(steps, dvStep{Name: "asterisk_restart", Detail: err.Error()})
			fail("asterisk_restart", "Asterisk did not restart: "+err.Error(), nil)
			return
		}
		undo.restarted = true
		if err := s.waitAsteriskBack(ctx); err != nil {
			steps = append(steps, dvStep{Name: "asterisk_restart", Detail: err.Error()})
			fail("asterisk_restart", err.Error(), nil)
			return
		}
		steps = append(steps, dvStep{Name: "asterisk_restart", OK: true})
	}
	// 5. the engine re-reads its config
	if s.dvEnabled() {
		if err := s.dvReloadEngine(ctx); err != nil {
			steps = append(steps, dvStep{Name: "engine_reload", Detail: err.Error()})
			fail("engine_reload", "the engine did not reload: "+err.Error(), nil)
			return
		}
		steps = append(steps, dvStep{Name: "engine_reload", OK: true})
	}
	// 6. health (the removed node is no longer there to check)
	healthNode := plan.Node
	if plan.Kind == dvKindRemove {
		healthNode = ""
	}
	h := s.dvPrivHealth(ctx, healthNode, plan.OldNode)
	if !h.OK {
		steps = append(steps, dvStep{Name: "health", Detail: "a check failed"})
		fail("health", "the health check failed after the change", h)
		return
	}
	steps = append(steps, dvStep{Name: "health", OK: true})
	if s.d.DVLink != nil {
		s.d.DVLink.Saved(ctx, undo.prev, plan.Link)
	}
	switch plan.Kind {
	case dvKindRemove:
		if s.d.Store != nil { // the name belonged to the removed node
			_ = s.d.Store.SetSetting(dvpanel.SettingNodeName, "")
		}
		s.audit(u, "dv.private.remove", map[string]any{"node": plan.Node, "restart": plan.RestartAsterisk, "keepRecord": plan.KeepRecord, "unlinked": len(plan.Unlinks)})
		s.d.Logger.Printf("dv private node %s removed by %s (record kept: %v)", plan.Node, u.Username, plan.KeepRecord)
	default:
		s.audit(u, "dv.private", map[string]any{"node": plan.Node, "kind": plan.Kind, "from": plan.OldNode, "layout": plan.Layout, "link": plan.Link.Mode, "restart": plan.RestartAsterisk})
		s.d.Logger.Printf("dv private node %s applied by %s (%s, %s)", plan.Node, u.Username, plan.Kind, plan.Layout)
	}
	s.dvs.invalidate()
	s.dvs.refreshSoon()
	writeJSON(w, 200, map[string]any{"applied": true, "kind": plan.Kind, "node": plan.Node, "steps": steps, "health": h, "warnings": plan.Warnings,
		"keepRecord": plan.KeepRecord, "rollback": s.dvPrivRollbackInfo()})
}

// dvPrivHealth checks node (when set) and the monitored nodes that still exist
// in rpt.conf as the apply wrote it: after a renumber or removal the monitor
// may still list the old bridge node, which no longer answers.
func (s *Server) dvPrivHealth(ctx context.Context, node, oldNode string) configsvc.Health {
	var nodes []string
	if node != "" {
		nodes = append(nodes, node)
	}
	if m := s.monitor(); m != nil {
		present := s.dvRptNodes(ctx)
		for _, n := range m.Nodes() {
			if n == oldNode && n != node {
				continue
			}
			if present != nil && !present[n] {
				continue
			}
			nodes = append(nodes, n)
		}
	}
	h := configsvc.Health{OK: true, Checks: []configsvc.Check{}}
	if svc := s.d.ConfigEditor; svc != nil {
		h = svc.HealthCheck(ctx, dedupe(nodes))
	}
	return h
}

// dvRptNodes is the set of numeric node stanzas in rpt.conf as it is now, or
// nil when it cannot be read.
func (s *Server) dvRptNodes(ctx context.Context) map[string]bool {
	svc := s.d.ConfigEditor
	if svc == nil {
		return nil
	}
	c, err := svc.Read(ctx, dvRptFile)
	if err != nil {
		return nil
	}
	f, err := astconf.Parse(dvRptFile, []byte(c.Content))
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for _, sec := range f.Sections() {
		if !sec.Template && isAllDigits(sec.Name) {
			out[sec.Name] = true
		}
	}
	return out
}

// waitAsteriskBack waits, up to AsteriskBackWait and polling every
// AsteriskBackPoll, until AMI is connected again and Asterisk lists a local
// node, after a restart (which drops the AMI connection). The bridge must not
// be restarted against a down Asterisk. The health check that follows still
// verifies the nodes themselves.
func (s *Server) waitAsteriskBack(ctx context.Context) error {
	wait, poll := s.AsteriskBackWait, s.AsteriskBackPoll
	if poll <= 0 {
		poll = time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if s.asteriskAnswers(ctx) {
			return nil
		}
		if ctx.Err() != nil {
			return fmt.Errorf("Asterisk did not come back: %w", ctx.Err())
		}
		if !time.Now().Add(poll).Before(deadline) {
			return fmt.Errorf("Asterisk did not come back within %d s", int(wait.Round(time.Second)/time.Second))
		}
		select {
		case <-ctx.Done():
		case <-time.After(poll):
		}
	}
}

func (s *Server) asteriskAnswers(ctx context.Context) bool {
	if !s.amiUp() {
		return false
	}
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	nodes, err := s.rpt.LocalNodes(cctx)
	return err == nil && len(nodes) > 0
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
	return map[string]any{"available": true, "before": u.before}
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
	var rerr error
	if undo.hadRecord != nil {
		_, rerr = s.d.DVEngine.DVPrivateSet(ctx, *undo.hadRecord)
	} else {
		_, rerr = s.d.DVEngine.DVPrivateClear(ctx)
	}
	if rerr != nil {
		problems = append(problems, "record: "+rerr.Error())
	}
	if s.d.Store != nil {
		_ = dvpanel.WriteLinkSettings(s.d.Store, undo.prev)
	}
	return problems
}

func (s *Server) handleDVPrivateRollback(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.d.DVEngine == nil {
		s.dvError(w, errDVUnavailable)
		return
	}
	if s.d.DVLink != nil {
		defer s.d.DVLink.Hold()() // no automatic relink until this is done (Saved arms the exclusive)
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
		err := s.restartAsteriskNow(ctx)
		if err == nil {
			err = s.waitAsteriskBack(ctx)
		}
		if err != nil {
			steps = append(steps, dvStep{Name: "asterisk_restart", Detail: err.Error()})
		} else {
			steps = append(steps, dvStep{Name: "asterisk_restart", OK: true})
		}
	}
	if (undo.restarted || undo.cfgDone) && s.dvEnabled() {
		err := s.dvRenderConfig(ctx)
		if err == nil {
			err = s.dvReloadEngine(ctx)
		}
		if err != nil {
			steps = append(steps, dvStep{Name: "engine_reload", Detail: err.Error()})
		} else {
			steps = append(steps, dvStep{Name: "engine_reload", OK: true})
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
	s.audit(u, "dv.private.rollback", map[string]any{"problems": len(problems)})
	s.d.Logger.Printf("dv private node rolled back by %s (%d problems)", u.Username, len(problems))
	s.dvs.invalidate()
	s.dvs.refreshSoon()
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
	// Adoptable: the node was found but not added by ASLCP, and it looks
	// exactly like the one ASLCP adds, so "Manage this bridge node with
	// ASLCP" (POST /api/dv/private-node/adopt) would work.
	Adoptable bool `json:"adoptable"`
	// Suggested is the first free number of 1900-1997 ("" when none is left).
	Suggested  string               `json:"suggested"`
	Used       []string             `json:"used"`
	LocalNodes []string             `json:"localNodes"`
	RxPort     int                  `json:"rxPort"`
	TxPort     int                  `json:"txPort"`
	Link       dvpanel.LinkSettings `json:"link"`
	Paused     bool                 `json:"paused"`
	// Name is the display name of the bridge node (the default when unset).
	Name string `json:"name"`
}

func (s *Server) dvBridgeSetup(ctx context.Context, v dvSetup) DVBridgeSetup {
	b := DVBridgeSetup{Used: []string{}, LocalNodes: []string{}, RxPort: dvpanel.DefaultRxPort, TxPort: dvpanel.DefaultTxPort}
	if s.d.ConfigEditor != nil {
		if t, err := s.d.ConfigEditor.Tree(ctx, dvRptFile); err == nil {
			used := dvpanel.UsedNodes(t)
			for n := range used {
				b.Used = append(b.Used, n)
			}
			sort.Strings(b.Used)
			b.Suggested = dvpanel.SuggestNode(used)
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
		b.Adoptable = s.dvAdoptable(ctx, v)
	}
	if b.Node != "" && v.USRP.RxPort != 0 && v.USRP.TxPort != 0 && v.USRP.RxPort != v.USRP.TxPort {
		b.RxPort, b.TxPort = v.USRP.RxPort, v.USRP.TxPort
	}
	b.Name = s.dvNodeName()
	b.Link = s.dvLinkSettings()
	b.Link.Bridge = b.Node
	if b.Link.LocalNode == "" {
		b.Link.LocalNode = s.dvLocalFor(dvpanel.LinkSettings{Mode: dvpanel.LinkOnDemand}, b.Node)
	}
	if s.d.Store != nil {
		p, _ := s.d.Store.GetSetting(dvpanel.SettingLinkPaused)
		b.Paused = p == "1"
	}
	return b
}
