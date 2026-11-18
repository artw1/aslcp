package api_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami/amitest"
	"github.com/artw1/aslcp/internal/api"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/dvswitch"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/rpt"
)

// Tests of the private bridge node (docs/DVSWITCH.md, M2). The local node of
// the test environment is 1997; the bridge node is 1996.

var reLoad = regexp.MustCompile(`(?m)^\+?load\s+= chan_usrp\.so`)

const (
	tmplRpt = `[general]

[node-main](!)
duplex = 1
context = radio
controlstates = controlstates
functions = functions-main
telemetry = telemetry-main

[1997](node-main)
rxchannel = SimpleUSB/usb
callsign = N0CALL
`
	flatRpt = `[1997]
rxchannel = SimpleUSB/usb
callsign = N0CALL
duplex = 1
context = radio
functions = functions-1997
telemetry = telemetry-1997
`
	modConf = "[modules]\nautoload = yes\nnoload = chan_usrp.so\n"
)

// logHelper notes the config writes in the order log of the fake DVSwitch.
type logHelper struct {
	*configsvc.DemoHelper
	dv *fakeDV
}

func (h logHelper) ConfigApply(ctx context.Context, files []helper.ConfigFile, msg, author string) (helper.ConfigApplyResult, error) {
	h.dv.note("config-apply")
	return h.DemoHelper.ConfigApply(ctx, files, msg, author)
}

func (h logHelper) ConfigRestore(ctx context.Context, commit, author string) (helper.ConfigApplyResult, error) {
	h.dv.note("config-restore")
	return h.DemoHelper.ConfigRestore(ctx, commit, author)
}

// okNodes answers every rpt status unless fail is set.
type okNodes struct {
	fail *atomic.Bool
	bad  *sync.Map // nodes whose status never answers
}

func (n okNodes) LocalNodes(context.Context) ([]string, error) { return []string{localNode}, nil }
func (n okNodes) XStat(_ context.Context, node string) (*rpt.XStat, error) {
	if n.fail.Load() {
		return nil, errors.New("no answer")
	}
	if _, bad := n.bad.Load(node); bad {
		return nil, errors.New("no such node")
	}
	return &rpt.XStat{Node: node}, nil
}

type privEnv struct {
	*env
	dv   *fakeDV
	dh   *configsvc.DemoHelper
	fail *atomic.Bool
	bad  *sync.Map
}

func newPrivEnv(t *testing.T, rptConf string, dvlink bool) *privEnv {
	t.Helper()
	return newPrivEnvNode(t, rptConf, dvlink, nil)
}

// newPrivEnvNode is newPrivEnv with a detector for the bridge node that is in
// rpt.conf (the snapshot's view of a USRP node, owned by ASLCP or not).
func newPrivEnvNode(t *testing.T, rptConf string, dvlink bool, dvNode func(context.Context) (string, bool)) *privEnv {
	t.Helper()
	pe := &privEnv{dv: &fakeDV{}, fail: new(atomic.Bool), bad: new(sync.Map)}
	pe.env = newEnv(t, opts{dvswitch: pe.dv, dvInstalled: installed, dvlink: dvlink, dvNode: dvNode, configFn: func(c rpt.API) *configsvc.Service {
		pe.dh = configsvc.NewDemoHelper()
		pe.dh.SetFile("rpt.conf", rptConf)
		pe.dh.SetFile("modules.conf", modConf)
		s := configsvc.New(logHelper{pe.dh, pe.dv}, c.(configsvc.AMI), okNodes{pe.fail, pe.bad})
		s.HealthTimeout, s.HealthPoll = 300*time.Millisecond, 20*time.Millisecond
		return s
	}})
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 31001, TxPort: 31001} // the stock ini
	pe.dv.reserved = []int{31100, 31103, 35100}
	return pe
}

func (pe *privEnv) file(name string) string {
	pe.t.Helper()
	c, err := pe.dh.ConfigRead(context.Background(), name)
	if err != nil {
		pe.t.Fatal(err)
	}
	return c.Content
}

func (pe *privEnv) plan(body map[string]any) (int, api.DVPlan) {
	pe.t.Helper()
	body["dryRun"] = true
	var p api.DVPlan
	c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &p)
	return c, p
}

func (pe *privEnv) code(body map[string]any) (int, string) {
	pe.t.Helper()
	return pe.errCode("admin", "PUT", "/api/dvswitch/private-node", body)
}

func planFile(p api.DVPlan, path string) (api.DVPlanFile, bool) {
	for _, f := range p.Files {
		if f.Path == path {
			return f, true
		}
	}
	return api.DVPlanFile{}, false
}

func TestDVPrivatePlanTemplateLayout(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	c, p := pe.plan(map[string]any{"node": "1996", "localNode": "1997", "linkMode": "permanent"})
	if c != 200 {
		t.Fatalf("plan: %d", c)
	}
	if p.Layout != "template" || !p.RestartAsterisk || p.Node != "1996" || p.RxPort != 34001 || p.TxPort != 32001 {
		t.Fatalf("%+v", p)
	}
	rf, _ := planFile(p, "rpt.conf")
	for _, want := range []string{"+[1996](node-main)", "+rxchannel = USRP/127.0.0.1:34001:32001", "+duplex = 0", "+1996 = radio@127.0.0.1/1996,NONE", "[nodes]"} {
		if !strings.Contains(rf.Diff, want) {
			t.Errorf("rpt.conf diff lacks %q:\n%s", want, rf.Diff)
		}
	}
	mf, _ := planFile(p, "modules.conf")
	if !mf.Changed || !strings.Contains(mf.Diff, "-noload = chan_usrp.so") || !reLoad.MatchString(mf.Diff) {
		t.Errorf("modules.conf diff:\n%s", mf.Diff)
	}
	ini, _ := planFile(p, "Analog_Bridge.ini")
	if !ini.Changed || !strings.Contains(ini.Diff, "+rxPort = 34001") || !strings.Contains(ini.Diff, "+txPort = 32001") || !strings.Contains(ini.Diff, "-rxPort = 31001") {
		t.Errorf("ini diff:\n%s", ini.Diff)
	}
	if strings.Join(p.Restarts, ",") != "analog_bridge,mmdvm_bridge" || p.Base["rpt.conf"] == "" || p.Base["analog_bridge"] != "ab01" {
		t.Errorf("%+v", p)
	}
	// a dry run writes nothing
	if got := pe.dv.orderOf(); len(got) != 0 {
		t.Fatalf("dry run called %v", got)
	}
	if pe.file("rpt.conf") != tmplRpt || pe.file("modules.conf") != modConf {
		t.Fatal("dry run changed a file")
	}
}

func TestDVPrivatePlanFlatLayout(t *testing.T) {
	pe := newPrivEnv(t, flatRpt, false)
	c, p := pe.plan(map[string]any{"node": "1996", "localNode": "1997"})
	if c != 200 || p.Layout != "flat" {
		t.Fatalf("%d %+v", c, p)
	}
	rf, _ := planFile(p, "rpt.conf")
	for _, want := range []string{"+[1996]", "+rxchannel = USRP/127.0.0.1:34001:32001", "+functions = functions-1997", "+telemetry = telemetry-1997", "+context = radio"} {
		if !strings.Contains(rf.Diff, want) {
			t.Errorf("diff lacks %q:\n%s", want, rf.Diff)
		}
	}
	if len(p.Warnings) == 0 {
		t.Error("a flat layout should say that stanza names are copied")
	}
}

func TestDVPrivateNodeValidation(t *testing.T) {
	rptConf := tmplRpt + "\n[nodes]\n1995 = radio@127.0.0.1/1995,NONE\n"
	pe := newPrivEnv(t, rptConf, false)
	for _, n := range []string{"", "abc", "0", "2000", "2006", "19990", "-5"} {
		if c, code := pe.code(map[string]any{"node": n, "dryRun": true}); c != 400 || code != "invalid_node" {
			t.Errorf("node %q: %d %s", n, c, code)
		}
	}
	// in use: a stanza, a [nodes] key, a template name is not a node
	for _, n := range []string{"1997", "1995"} {
		if c, code := pe.code(map[string]any{"node": n, "dryRun": true}); c != 409 || code != "node_in_use" {
			t.Errorf("node %s: %d %s", n, c, code)
		}
	}
	// 1998 and 1999 are allowed, without a warning
	for _, n := range []string{"1998", "1999"} {
		if c, p := pe.plan(map[string]any{"node": n}); c != 200 || len(p.Warnings) != 0 {
			t.Errorf("node %s: %d %v", n, c, p.Warnings)
		}
	}
	if c, p := pe.plan(map[string]any{"node": "1996"}); c != 200 || len(p.Warnings) != 0 {
		t.Errorf("1996: %d %v", c, p.Warnings)
	}
	// link settings
	if c, code := pe.code(map[string]any{"node": "1996", "linkMode": "sometimes", "dryRun": true}); c != 400 || code != "bad_request" {
		t.Errorf("link mode: %d %s", c, code)
	}
	if c, code := pe.code(map[string]any{"node": "1996", "linkMode": "permanent", "dryRun": true}); c != 400 || code != "bad_request" {
		t.Errorf("permanent without a local node: %d %s", c, code)
	}
	if c, code := pe.code(map[string]any{"node": "1996", "localNode": "1234", "dryRun": true}); c != 400 || code != "bad_request" {
		t.Errorf("not a local node: %d %s", c, code)
	}
}

func TestDVPrivateNodeSuggestion(t *testing.T) {
	rptConf := tmplRpt + "\n[1900]\nrxchannel = USRP/127.0.0.1:36001:37001\n\n[nodes]\n1901 = radio@127.0.0.1/1901,NONE\n"
	pe := newPrivEnv(t, rptConf, false)
	var v struct {
		BridgeNode api.DVBridgeSetup `json:"bridgeNode"`
	}
	if c := pe.do("admin", "GET", "/api/dvswitch/setup", nil, &v); c != 200 {
		t.Fatal(c)
	}
	b := v.BridgeNode
	if b.Suggested != "1902" || b.RxPort != 34001 || b.TxPort != 32001 || b.Owned || b.Node != "" {
		t.Fatalf("%+v", b)
	}
	if strings.Join(b.Used, ",") != "1900,1901,1997" || strings.Join(b.LocalNodes, ",") != "1997" || b.Link.Mode != "ondemand" {
		t.Fatalf("%+v", b)
	}
}

func TestDVPrivatePortConflict(t *testing.T) {
	rptConf := tmplRpt + "\n[1995]\nrxchannel = USRP/127.0.0.1:36001:37001\n"
	pe := newPrivEnv(t, rptConf, false)
	cases := []struct {
		rx, tx int
		status int
		code   string
	}{
		{31100, 32001, 409, "port_conflict"}, // a reserved DVSwitch port
		{34001, 35100, 409, "port_conflict"},
		{36001, 32001, 409, "port_conflict"}, // another USRP node
		{34001, 37001, 409, "port_conflict"},
		{34001, 34001, 400, "invalid_field"},
		{80, 32001, 400, "invalid_field"},
		{34001, 70000, 400, "invalid_field"},
	}
	for _, c := range cases {
		st, code := pe.code(map[string]any{"node": "1996", "rxPort": c.rx, "txPort": c.tx, "dryRun": true})
		if st != c.status || code != c.code {
			t.Errorf("%d/%d: %d %s", c.rx, c.tx, st, code)
		}
	}
	if st, _ := pe.plan(map[string]any{"node": "1996", "rxPort": 34002, "txPort": 32002}); st != 200 {
		t.Errorf("free ports: %d", st)
	}
}

func TestDVPrivateApplyOrderAndFiles(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	before := countCmd(pe.env, "core restart gracefully")
	pe.dv.onRestart = func() {
		pe.dv.note(fmt.Sprintf("asterisk-restarts=%d", countCmd(pe.env, "core restart gracefully")-before))
	}
	body := map[string]any{"node": "1996", "localNode": "1997", "linkMode": "permanent", "restart": true}
	var out struct {
		Applied bool
		Steps   []struct {
			Name string
			OK   bool
		}
		Health   struct{ OK bool }
		Rollback struct{ Available bool }
	}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != 200 || !out.Applied || !out.Health.OK || !out.Rollback.Available {
		t.Fatalf("apply: %d %+v", c, out)
	}
	// Asterisk files, then the ini, the record, the Asterisk restart (seen
	// from the bridge restart) and the bridge restart
	got := strings.Join(pe.dv.orderOf(), ",")
	if want := "config-apply,usrp,private:set,asterisk-restarts=1,restart"; got != want {
		t.Fatalf("order %s, want %s", got, want)
	}
	var names []string
	for _, s := range out.Steps {
		names = append(names, s.Name)
		if !s.OK {
			t.Errorf("step %s failed", s.Name)
		}
	}
	if strings.Join(names, ",") != "asterisk_config,ini,asterisk_restart,bridge_restart,health" {
		t.Fatalf("%v", names)
	}
	rptNow, mods := pe.file("rpt.conf"), pe.file("modules.conf")
	if !strings.Contains(rptNow, "[1996](node-main)") || !strings.Contains(rptNow, "1996 = radio@127.0.0.1/1996,NONE") {
		t.Fatalf("rpt.conf:\n%s", rptNow)
	}
	if !reLoad.MatchString(mods) || strings.Contains(mods, "noload") {
		t.Fatalf("modules.conf:\n%s", mods)
	}
	if c := pe.dv.usrpCalls; len(c) != 1 || c[0] != (helper.DVUSRP{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}) {
		t.Fatalf("usrp %+v", c)
	}
	rec := pe.dv.priv
	if rec == nil || rec.Node != "1996" || !rec.StanzaAdded || !rec.NodesLineAdded || rec.ChanUsrpWas != "noload" ||
		!strings.HasPrefix(rec.StanzaText, "[1996](node-main)") {
		t.Fatalf("record %+v", rec)
	}
	l := dvswitch.ReadLinkSettings(pe.st)
	if l.Mode != "permanent" || l.LocalNode != "1997" || l.Bridge != "1996" {
		t.Fatalf("settings %+v", l)
	}
	// the audit log names the node, no content
	if !auditHas(pe.env, "dvswitch.private") {
		t.Error("no audit entry")
	}
}

func auditHas(e *env, action string) bool {
	log, err := e.st.ListAudit(100)
	if err != nil {
		return false
	}
	for _, a := range log {
		if a.Action == action {
			return true
		}
	}
	return false
}

func TestDVPrivateRestartRequired(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	for _, body := range []map[string]any{{"node": "1996"}, {"node": "1996", "restart": false}} {
		if c, code := pe.code(body); c != 409 || code != "restart_required" {
			t.Fatalf("%d %s", c, code)
		}
	}
	if got := pe.dv.orderOf(); len(got) != 0 || pe.file("rpt.conf") != tmplRpt {
		t.Fatalf("something was written: %v", got)
	}
	if countCmd(pe.env, "core restart gracefully") != 0 {
		t.Fatal("Asterisk was restarted")
	}
}

func TestDVPrivateResaveNeedsNoRestart(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	body := map[string]any{"node": "1996", "localNode": "1997", "restart": true}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, nil); c != 200 {
		t.Fatal(c)
	}
	rptNow := pe.file("rpt.conf")
	// the same setup again, with another link mode: only the settings change
	pe.dh.SetFile("rpt.conf", rptNow)
	body = map[string]any{"node": "1996", "localNode": "1997", "linkMode": "permanent"}
	var p api.DVPlan
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", map[string]any{"node": "1996", "localNode": "1997", "dryRun": true}, &p); c != 200 || p.RestartAsterisk || len(p.Restarts) != 0 {
		t.Fatalf("%d %+v", c, p)
	}
	n := len(pe.dv.orderOf())
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, nil); c != 200 {
		t.Fatalf("re-save: %d", c)
	}
	if got := pe.dv.orderOf(); len(got) != n+1 || got[n] != "private:set" {
		t.Fatalf("%v", got)
	}
	if dvswitch.ReadLinkSettings(pe.st).Mode != "permanent" || countCmd(pe.env, "core restart gracefully") != 1 {
		t.Fatal("settings or restarts wrong")
	}
	if pe.dv.priv.StanzaText == "" || !pe.dv.priv.StanzaAdded {
		t.Fatalf("the record lost what ASLCP added: %+v", pe.dv.priv)
	}
}

func TestDVPrivateConflict(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	body := map[string]any{"node": "1996", "restart": true, "base": map[string]string{"rpt.conf": "0000"}}
	var out map[string]any
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != 409 || out["code"] != "conflict" {
		t.Fatalf("%d %v", c, out)
	}
	if len(pe.dv.orderOf()) != 0 {
		t.Fatal("written despite the conflict")
	}
}

func TestDVPrivateHealthFailureOffersRollback(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	pe.fail.Store(true)
	body := map[string]any{"node": "1996", "localNode": "1997", "restart": true}
	var out struct {
		Code, Step string
		Applied    bool
		Rollback   struct{ Available bool }
	}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != 502 || out.Code != "apply_failed" || out.Step != "health" || !out.Applied || !out.Rollback.Available {
		t.Fatalf("%d %+v", c, out)
	}
	if !strings.Contains(pe.file("rpt.conf"), "[1996]") {
		t.Fatal("the change should stay until the operator decides")
	}
	pe.fail.Store(false)
	var rb struct {
		RolledBack bool
		Problems   []string
	}
	if c := pe.do("admin", "POST", "/api/dvswitch/private-node/rollback", map[string]any{}, &rb); c != 200 || !rb.RolledBack || len(rb.Problems) != 0 {
		t.Fatalf("rollback: %d %+v", c, rb)
	}
	if pe.file("rpt.conf") != tmplRpt || pe.file("modules.conf") != modConf {
		t.Fatal("the Asterisk files were not restored")
	}
	if pe.dv.priv != nil {
		t.Fatalf("record left: %+v", pe.dv.priv)
	}
	got := strings.Join(pe.dv.orderOf(), ",")
	for _, want := range []string{"config-restore", "rollback", "private:clear"} {
		if !strings.Contains(got, want) {
			t.Errorf("order %s lacks %s", got, want)
		}
	}
	if countCmd(pe.env, "core restart gracefully") != 2 {
		t.Fatalf("Asterisk restarts: %d", countCmd(pe.env, "core restart gracefully"))
	}
	if l := dvswitch.ReadLinkSettings(pe.st); l.Bridge != "" || l.LocalNode != "" {
		t.Fatalf("settings not restored: %+v", l)
	}
	// nothing left to roll back
	expectErr(t, pe.env, "admin", "POST", "/api/dvswitch/private-node/rollback", map[string]any{}, 404, "no_rollback")
}

func TestDVPrivateIniFailureRestoresConfig(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	pe.dv.usrpErr = helper.ErrDVNotInstalled
	if c, code := pe.code(map[string]any{"node": "1996", "restart": true}); c != 409 || code != "not_installed" {
		t.Fatalf("%d %s", c, code)
	}
	if pe.file("rpt.conf") != tmplRpt || pe.file("modules.conf") != modConf {
		t.Fatal("the Asterisk files were not restored after the ini failed")
	}
	if countCmd(pe.env, "core restart gracefully") != 0 || pe.dv.priv != nil {
		t.Fatal("restart or record after a failed ini")
	}
}

func TestDVPrivateRoles(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	for _, role := range []string{"", "viewer", "op"} {
		want, code := 403, "forbidden"
		if role == "" {
			want, code = 401, "unauthorized"
		}
		expectErr(t, pe.env, role, "PUT", "/api/dvswitch/private-node", map[string]any{"node": "1996", "dryRun": true}, want, code)
		expectErr(t, pe.env, role, "POST", "/api/dvswitch/private-node/rollback", map[string]any{}, want, code)
	}
	if len(pe.dv.orderOf()) != 0 {
		t.Fatal("a non-admin reached the helper")
	}
	// operators link and unlink the node with the ordinary connect routes;
	// viewers may not
	if c := pe.do("op", "POST", "/api/nodes/"+localNode+"/connect", map[string]any{"target": "1996", "permanent": false}, nil); c != 202 {
		t.Fatalf("operator connect: %d", c)
	}
	if c := pe.do("op", "POST", "/api/nodes/"+localNode+"/disconnect", map[string]any{"target": "1996"}, nil); c != 202 {
		t.Fatalf("operator disconnect: %d", c)
	}
	expectErr(t, pe.env, "viewer", "POST", "/api/nodes/"+localNode+"/connect", map[string]any{"target": "1996"}, 403, "forbidden")
}

// ---- snapshot and reconciler

func dvLinkEnv(t *testing.T, reconcile bool) *env {
	f := newCtlDV()
	return newEnv(t, opts{dvswitch: f, dvInstalled: installed, dvlink: reconcile,
		dvNode: func(context.Context) (string, bool) { return "1996", true }})
}

func TestDVSnapshotLink(t *testing.T) {
	e := dvLinkEnv(t, false)
	var sn api.DVSnapshot
	if c := e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 {
		t.Fatal(c)
	}
	if !sn.BridgeNode.Present || sn.BridgeNode.Node != "1996" || sn.Link.Mode != "ondemand" || sn.Link.LocalNode != localNode || sn.Link.Linked || sn.Link.Paused {
		t.Fatalf("%+v %+v", sn.BridgeNode, sn.Link)
	}
	// permanent mode with a chosen node; linked once the node is in its links
	if err := dvswitch.WriteLinkSettings(e.st, dvswitch.LinkSettings{Mode: "permanent", LocalNode: localNode, Bridge: "1996"}); err != nil {
		t.Fatal(err)
	}
	e.fake.mu.Lock()
	e.fake.links = append(e.fake.links, "1996")
	e.fake.mu.Unlock()
	waitFor(t, func() bool {
		e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
		return sn.Link.Linked
	})
	if sn.Link.Mode != "permanent" || sn.Link.LocalNode != localNode {
		t.Fatalf("%+v", sn.Link)
	}
}

func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("timed out")
}

func ilink13(e *env) int {
	n := 0
	for _, c := range e.fake.ilinkCommands() {
		if strings.Contains(c, "ilink 13 1996") {
			n++
		}
	}
	return n
}

func TestDVReconcilerViaAPI(t *testing.T) {
	e := dvLinkEnv(t, true)
	if err := dvswitch.WriteLinkSettings(e.st, dvswitch.LinkSettings{Mode: "permanent", LocalNode: localNode, Bridge: "1996"}); err != nil {
		t.Fatal(err)
	}
	// the node is not among the links: the reconciler issues ilink 13
	waitFor(t, func() bool { return ilink13(e) >= 1 })
	// a manual disconnect in the panel pauses it
	if c := e.do("op", "POST", "/api/nodes/"+localNode+"/disconnect", map[string]any{"target": "1996", "permanent": true}, nil); c != 202 {
		t.Fatalf("disconnect: %d", c)
	}
	waitFor(t, func() bool { v, _ := e.st.GetSetting(dvswitch.SettingLinkPaused); return v == "1" })
	n := ilink13(e)
	time.Sleep(600 * time.Millisecond)
	if ilink13(e) != n {
		t.Fatalf("the reconciler linked again while paused (%d -> %d)", n, ilink13(e))
	}
	var sn api.DVSnapshot
	e.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn)
	if !sn.Link.Paused {
		t.Fatalf("%+v", sn.Link)
	}
	// linking it again by hand ends the pause
	if c := e.do("op", "POST", "/api/nodes/"+localNode+"/connect", map[string]any{"target": "1996", "permanent": true}, nil); c != 202 {
		t.Fatalf("connect: %d", c)
	}
	waitFor(t, func() bool { v, _ := e.st.GetSetting(dvswitch.SettingLinkPaused); return v != "1" })
	// a disconnect of another node is none of its business
	if c := e.do("op", "POST", "/api/nodes/"+localNode+"/disconnect", map[string]any{"target": "50241"}, nil); c != 202 {
		t.Fatal(c)
	}
	time.Sleep(100 * time.Millisecond)
	if v, _ := e.st.GetSetting(dvswitch.SettingLinkPaused); v == "1" {
		t.Fatal("paused by another node's disconnect")
	}
}

// ---- uninstall

func TestDVUninstallUnlinksPermanentLink(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, true)
	f := pe.dv
	// the ordinary fakeDV reports no installation, the uninstall call is
	// all that matters here
	if err := dvswitch.WriteLinkSettings(pe.st, dvswitch.LinkSettings{Mode: "permanent", LocalNode: localNode, Bridge: "1996"}); err != nil {
		t.Fatal(err)
	}
	pe.fake.mu.Lock()
	pe.fake.links = append(pe.fake.links, "1996") // linked: no reconnect noise
	pe.fake.mu.Unlock()
	if c := pe.do("admin", "POST", "/api/dvswitch/uninstall", nil, nil); c != 202 {
		t.Fatalf("uninstall: %d", c)
	}
	if f.uninstalls != 1 {
		t.Fatal("no uninstall")
	}
	var disc []string
	for _, c := range pe.fake.ilinkCommands() {
		if strings.Contains(c, "ilink 11 1996") {
			disc = append(disc, c)
		}
	}
	if len(disc) != 1 {
		t.Fatalf("the permanent link was not removed first: %v", pe.fake.ilinkCommands())
	}
	if l := dvswitch.ReadLinkSettings(pe.st); l.Bridge != "" || l.Mode != "ondemand" {
		t.Fatalf("settings stay after the uninstall: %+v", l)
	}
	if v, _ := pe.st.GetSetting(dvswitch.SettingLinkPaused); v == "1" {
		t.Fatal("the unlink counted as a manual disconnect")
	}
}

// A real idle Asterisk restarts at once and hangs up before replying to
// "core restart gracefully": the apply must count that as a restart, wait for
// Asterisk to answer again and only then restart the bridge.
func TestDVPrivateApplyAsteriskDropsConnectionOnRestart(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	pe.srv.AsteriskBackWait, pe.srv.AsteriskBackPoll = 5*time.Second, 20*time.Millisecond
	var down atomic.Bool
	pe.fake.Handle("RptLocalNodes", func(_ *amitest.Session, r *amitest.Request) string {
		if down.Load() {
			return amitest.Error(r, "starting")
		}
		return amitest.Raw(`<?xml version="1.0"?>`, `<nodes>`, `  <node>`+localNode+`</node>`, `</nodes>`)
	})
	pe.fake.Handle("Command", func(s *amitest.Session, r *amitest.Request) string {
		cmd := r.Get("Command")
		pe.fake.mu.Lock()
		pe.fake.commands = append(pe.fake.commands, cmd)
		pe.fake.mu.Unlock()
		if cmd == "core restart gracefully" {
			down.Store(true)
			go func() { time.Sleep(300 * time.Millisecond); down.Store(false) }()
			s.Close()
			return ""
		}
		return amitest.CommandOutput(r, "System uptime: 5")
	})
	var bridgeAt time.Time
	restarted := time.Now()
	pe.dv.onRestart = func() { bridgeAt = time.Now() }
	body := map[string]any{"node": "1996", "localNode": "1997", "restart": true}
	var out struct {
		Applied bool
		Steps   []struct {
			Name string
			OK   bool
		}
		Health struct{ OK bool }
	}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != 200 || !out.Applied || !out.Health.OK {
		t.Fatalf("apply: %d %+v", c, out)
	}
	for _, s := range out.Steps {
		if !s.OK {
			t.Errorf("step %s failed", s.Name)
		}
	}
	if countCmd(pe.env, "core restart gracefully") != 1 {
		t.Fatal("Asterisk was not restarted once")
	}
	if bridgeAt.IsZero() || bridgeAt.Sub(restarted) < 250*time.Millisecond {
		t.Fatalf("the bridge was restarted %v after the Asterisk restart, before it was back", bridgeAt.Sub(restarted))
	}
}

func TestDVPrivateApplyAsteriskNeverComesBack(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	pe.srv.AsteriskBackWait, pe.srv.AsteriskBackPoll = 200*time.Millisecond, 20*time.Millisecond
	pe.fake.Handle("RptLocalNodes", func(_ *amitest.Session, r *amitest.Request) string {
		if countCmd(pe.env, "core restart gracefully") > 0 {
			return amitest.Error(r, "down")
		}
		return amitest.Raw(`<?xml version="1.0"?>`, `<nodes>`, `  <node>`+localNode+`</node>`, `</nodes>`)
	})
	var out struct {
		Error string
		Step  string
	}
	body := map[string]any{"node": "1996", "localNode": "1997", "restart": true}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != http.StatusBadGateway || out.Step != "asterisk_restart" || !strings.Contains(out.Error, "did not come back within") {
		t.Fatalf("%d %+v", c, out)
	}
	for _, o := range pe.dv.orderOf() {
		if o == "restart" {
			t.Fatal("the bridge was restarted although Asterisk was down")
		}
	}
}

// ---- remove and renumber ----

// nodesRpt has the [nodes] section, so that a revert is byte for byte.
const nodesRpt = tmplRpt + "\n[nodes]\n"

func node1998(context.Context) (string, bool) { return "1998", true }

type dvApplyOut struct {
	Applied bool
	Kind    string
	Node    string
	Steps   []struct {
		Name string
		OK   bool
	}
	Warnings   []string
	KeepRecord bool
	Code, Step string
	Rollback   struct{ Available bool }
}

func (pe *privEnv) set(node string, extra map[string]any) {
	pe.t.Helper()
	body := map[string]any{"node": node, "localNode": "1997", "restart": true}
	for k, v := range extra {
		body[k] = v
	}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, nil); c != 200 {
		pe.t.Fatalf("set %s: %d", node, c)
	}
}

func (pe *privEnv) remove(body map[string]any) (int, dvApplyOut) {
	pe.t.Helper()
	var out dvApplyOut
	c := pe.do("admin", "DELETE", "/api/dvswitch/private-node", body, &out)
	return c, out
}

func TestDVPrivateRemoveHappyPath(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", map[string]any{"linkMode": "permanent"})
	if dvswitch.ReadLinkSettings(pe.st).Bridge != "1998" || pe.dv.priv == nil {
		t.Fatal("setup failed")
	}
	// the plan names every change and writes nothing
	var p api.DVPlan
	if c := pe.do("admin", "DELETE", "/api/dvswitch/private-node", map[string]any{"dryRun": true}, &p); c != 200 || p.Kind != "remove" || p.Node != "1998" || !p.RestartAsterisk || p.KeepRecord {
		t.Fatalf("plan %d %+v", c, p)
	}
	for _, f := range []string{"rpt.conf", "modules.conf"} {
		if pf, ok := planFile(p, "/etc/asterisk/"+f); !ok && !strings.Contains(fmt.Sprint(p.Files), f) {
			t.Fatalf("no diff for %s: %+v (%+v)", f, p.Files, pf)
		}
	}
	// without the confirmation nothing is touched
	if c, out := pe.remove(map[string]any{}); c != 409 {
		t.Fatalf("%d %+v", c, out)
	}
	if !strings.Contains(pe.file("rpt.conf"), "[1998]") {
		t.Fatal("removed without confirmation")
	}
	restarts := countCmd(pe.env, "core restart gracefully")
	pe.dv.mu.Lock()
	pe.dv.order, pe.dv.usrpCalls = nil, nil
	pe.dv.mu.Unlock()
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}

	c, out := pe.remove(map[string]any{"restart": true})
	if c != 200 || !out.Applied || out.Kind != "remove" || out.KeepRecord || len(out.Warnings) != 0 {
		t.Fatalf("%d %+v", c, out)
	}
	if pe.file("rpt.conf") != nodesRpt || pe.file("modules.conf") != modConf {
		t.Fatalf("not reverted:\n%s\n%s", pe.file("rpt.conf"), pe.file("modules.conf"))
	}
	if countCmd(pe.env, "core restart gracefully")-restarts != 1 {
		t.Fatal("expected exactly one Asterisk restart")
	}
	if len(pe.dv.usrpCalls) != 1 || pe.dv.usrpCalls[0].RxPort != 31001 || pe.dv.usrpCalls[0].TxPort != 31001 || !pe.dv.usrpCalls[0].Restore {
		t.Fatalf("ports not restored: %+v", pe.dv.usrpCalls)
	}
	if pe.dv.priv != nil {
		t.Fatalf("record kept: %+v", pe.dv.priv)
	}
	if l := dvswitch.ReadLinkSettings(pe.st); l.Bridge != "" || l.Mode == "permanent" {
		t.Fatalf("settings not cleared: %+v", l)
	}
	got := strings.Join(pe.dv.orderOf(), ",")
	if !strings.HasSuffix(got, "restart") || strings.Index(got, "usrp") > strings.Index(got, "private:clear") {
		t.Fatalf("order %s", got)
	}
	if !auditHas(pe.env, "dvswitch.private.remove") {
		t.Error("no audit entry")
	}
	// nothing left to remove
	expectErr(t, pe.env, "admin", "DELETE", "/api/dvswitch/private-node", map[string]any{"restart": true}, 409, "no_bridge_node")
}

func TestDVPrivateRemoveRecordWithoutPortsUsesStock(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", nil)
	rec := *pe.dv.priv
	rec.UsrpWas, rec.UsrpUntouched = nil, false // an older record
	pe.dv.priv = &rec
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}
	var p api.DVPlan
	if c := pe.do("admin", "DELETE", "/api/dvswitch/private-node", map[string]any{"dryRun": true}, &p); c != 200 {
		t.Fatal(c)
	}
	if !strings.Contains(strings.Join(p.Warnings, "|"), "stock values 31001 and 31001") {
		t.Fatalf("no stock-values notice: %v", p.Warnings)
	}
}

func TestDVPrivateRemoveHandEditedStanzaKeepsRecord(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", nil)
	pe.dh.SetFile("rpt.conf", strings.Replace(pe.file("rpt.conf"), "duplex = 0", "duplex = 0\nhangtime = 99", 1))
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}
	c, out := pe.remove(map[string]any{"restart": true})
	if c != 200 || !out.Applied || !out.KeepRecord || len(out.Warnings) == 0 || !strings.Contains(strings.Join(out.Warnings, "|"), "changed by hand") {
		t.Fatalf("%d %+v", c, out)
	}
	rptNow := pe.file("rpt.conf")
	if !strings.Contains(rptNow, "hangtime = 99") || !strings.Contains(rptNow, "[1998]") {
		t.Fatal("the edited stanza was removed")
	}
	if strings.Contains(rptNow, "1998 = radio@") {
		t.Fatal("the unchanged [nodes] line should be reverted")
	}
	if pe.dv.priv == nil || !pe.dv.priv.StanzaAdded || pe.dv.priv.NodesLineAdded {
		t.Fatalf("record should keep only the stanza: %+v", pe.dv.priv)
	}
	if !reLoad.MatchString(pe.file("modules.conf")) && strings.Contains(pe.file("modules.conf"), "noload") == false {
		t.Fatal("modules.conf in an unexpected state")
	}
}

func TestDVPrivateRemoveNotOwned(t *testing.T) {
	// a bridge node somebody else added is left alone
	pe := newPrivEnvNode(t, nodesRpt+"\n[1998]\nrxchannel = USRP/127.0.0.1:34001:32001\nduplex = 0\n", false, node1998)
	expectErr(t, pe.env, "admin", "DELETE", "/api/dvswitch/private-node", map[string]any{"restart": true}, 409, "not_owned")
	if !strings.Contains(pe.file("rpt.conf"), "[1998]") || len(pe.dv.orderOf()) != 0 {
		t.Fatal("touched a node ASLCP does not own")
	}
	// and with nothing at all
	pe = newPrivEnv(t, nodesRpt, false)
	expectErr(t, pe.env, "admin", "DELETE", "/api/dvswitch/private-node", map[string]any{"restart": true}, 409, "no_bridge_node")
}

func TestDVPrivateRemoveRoles(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", nil)
	for _, role := range []string{"", "viewer", "op"} {
		want, code := 403, "forbidden"
		if role == "" {
			want, code = 401, "unauthorized"
		}
		expectErr(t, pe.env, role, "DELETE", "/api/dvswitch/private-node", map[string]any{"restart": true}, want, code)
	}
	if pe.dv.priv == nil {
		t.Fatal("removed by an unauthorised user")
	}
}

func TestDVPrivateRenumberHappyPath(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", map[string]any{"linkMode": "permanent"})
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}
	was := pe.dv.priv.UsrpWas
	restarts := countCmd(pe.env, "core restart gracefully")
	pe.dv.mu.Lock()
	pe.dv.usrpCalls = nil
	pe.dv.mu.Unlock()

	// without renumber the same request is node_in_use, as before
	if c, code := pe.code(map[string]any{"node": "1999", "localNode": "1997", "restart": true}); c != 409 || code != "node_in_use" {
		t.Fatalf("%d %s", c, code)
	}
	var p api.DVPlan
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", map[string]any{"node": "1999", "renumber": true, "dryRun": true}, &p); c != 200 || p.Kind != "renumber" || p.OldNode != "1998" || p.Node != "1999" || !p.RestartAsterisk {
		t.Fatalf("plan %d %+v", c, p)
	}
	var out dvApplyOut
	body := map[string]any{"node": "1999", "renumber": true, "restart": true}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != 200 || !out.Applied || out.Kind != "renumber" || !out.Rollback.Available {
		t.Fatalf("%d %+v", c, out)
	}
	if countCmd(pe.env, "core restart gracefully")-restarts != 1 {
		t.Fatal("expected one Asterisk restart")
	}
	rptNow := pe.file("rpt.conf")
	if strings.Contains(rptNow, "1998") || !strings.Contains(rptNow, "[1999](node-main)") || !strings.Contains(rptNow, "1999 = radio@127.0.0.1/1999,NONE") {
		t.Fatalf("rpt.conf:\n%s", rptNow)
	}
	if !reLoad.MatchString(pe.file("modules.conf")) {
		t.Fatal("chan_usrp must stay loaded")
	}
	if len(pe.dv.usrpCalls) != 0 {
		t.Fatalf("ports unchanged but the ini was written: %+v", pe.dv.usrpCalls)
	}
	rec := pe.dv.priv
	if rec == nil || rec.Node != "1999" || !rec.StanzaAdded || !strings.HasPrefix(rec.StanzaText, "[1999]") || rec.ChanUsrpWas != "noload" {
		t.Fatalf("record %+v", rec)
	}
	if rec.UsrpWas == nil || was == nil || *rec.UsrpWas != *was {
		t.Fatalf("the pre-ASLCP ports were lost: %+v vs %+v", rec.UsrpWas, was)
	}
	l := dvswitch.ReadLinkSettings(pe.st)
	if l.Bridge != "1999" || l.Mode != "permanent" || l.LocalNode != "1997" {
		t.Fatalf("settings %+v", l)
	}
	if !auditHas(pe.env, "dvswitch.private") {
		t.Error("no audit entry")
	}
}

func TestDVPrivateRenumberRefusals(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	// nothing to renumber
	if c, code := pe.code(map[string]any{"node": "1999", "renumber": true, "restart": true}); c != 409 || code != "no_bridge_node" {
		t.Fatalf("%d %s", c, code)
	}
	pe.set("1998", nil)
	// the old stanza was edited by hand: refused, nothing written
	edited := strings.Replace(pe.file("rpt.conf"), "duplex = 0", "duplex = 0\nhangtime = 99", 1)
	pe.dh.SetFile("rpt.conf", edited)
	n := len(pe.dv.orderOf())
	restarts := countCmd(pe.env, "core restart gracefully")
	if c, code := pe.code(map[string]any{"node": "1999", "renumber": true, "restart": true}); c != 409 || code != "old_node_edited" {
		t.Fatalf("%d %s", c, code)
	}
	if pe.file("rpt.conf") != edited || len(pe.dv.orderOf()) != n || countCmd(pe.env, "core restart gracefully") != restarts {
		t.Fatal("something was changed by a refused renumber")
	}
	if pe.dv.priv == nil || pe.dv.priv.Node != "1998" {
		t.Fatalf("record changed: %+v", pe.dv.priv)
	}
}

func TestDVPrivateRenumberNotOwned(t *testing.T) {
	// 1999 is somebody else's bridge node: ASLCP owns nothing, the request
	// is node_in_use with or without renumber
	pe := newPrivEnvNode(t, nodesRpt+"\n[1999]\nrxchannel = USRP/127.0.0.1:34005:32005\nduplex = 0\n", false, func(context.Context) (string, bool) { return "1999", true })
	for _, renumber := range []bool{false, true} {
		c, code := pe.code(map[string]any{"node": "1998", "renumber": renumber, "restart": true})
		if c != 409 || (code != "node_in_use" && code != "no_bridge_node") {
			t.Fatalf("renumber=%v: %d %s", renumber, c, code)
		}
	}
	// ASLCP owns 1998; the target 1999 exists: node_in_use
	pe = newPrivEnv(t, nodesRpt, false)
	pe.set("1998", nil)
	pe.dh.SetFile("rpt.conf", pe.file("rpt.conf")+"\n[1999]\nrxchannel = SimpleUSB/usb2\n")
	if c, code := pe.code(map[string]any{"node": "1999", "renumber": true, "restart": true}); c != 409 || code != "node_in_use" {
		t.Fatalf("%d %s", c, code)
	}
}

func TestDVPrivateRenumberRollbackRestores(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", map[string]any{"linkMode": "permanent"})
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}
	rptBefore, modBefore := pe.file("rpt.conf"), pe.file("modules.conf")
	recBefore := *pe.dv.priv
	settingsBefore := dvswitch.ReadLinkSettings(pe.st)

	pe.fail.Store(true) // the health check fails after the restart
	var out dvApplyOut
	body := map[string]any{"node": "1999", "renumber": true, "restart": true}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != 502 || out.Code != "apply_failed" || out.Step != "health" || !out.Applied || !out.Rollback.Available {
		t.Fatalf("%d %+v", c, out)
	}
	if !strings.Contains(pe.file("rpt.conf"), "[1999]") || dvswitch.ReadLinkSettings(pe.st).Bridge != "1999" {
		t.Fatal("the change should stay until the operator decides")
	}
	pe.fail.Store(false)
	var rb struct {
		RolledBack bool
		Problems   []string
	}
	if c := pe.do("admin", "POST", "/api/dvswitch/private-node/rollback", map[string]any{}, &rb); c != 200 || !rb.RolledBack || len(rb.Problems) != 0 {
		t.Fatalf("rollback: %d %+v", c, rb)
	}
	if pe.file("rpt.conf") != rptBefore || pe.file("modules.conf") != modBefore {
		t.Fatal("the Asterisk files were not restored")
	}
	if pe.dv.priv == nil || *pe.dv.priv != recBefore {
		t.Fatalf("record not restored: %+v want %+v", pe.dv.priv, recBefore)
	}
	if l := dvswitch.ReadLinkSettings(pe.st); l != settingsBefore {
		t.Fatalf("settings not restored: %+v want %+v", l, settingsBefore)
	}
	if countCmd(pe.env, "core restart gracefully") != 3 { // set, renumber, rollback
		t.Fatalf("restarts: %d", countCmd(pe.env, "core restart gracefully"))
	}
}

func TestDVPrivateRemoveRollbackRestores(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", map[string]any{"linkMode": "permanent"})
	pe.dv.usrp = helper.DVUSRPView{Address: "127.0.0.1", RxPort: 34001, TxPort: 32001}
	rptBefore := pe.file("rpt.conf")
	recBefore := *pe.dv.priv
	settingsBefore := dvswitch.ReadLinkSettings(pe.st)
	pe.fail.Store(true)
	var out dvApplyOut
	// the bridge restart succeeds; the post-restart check of Asterisk fails
	c := pe.do("admin", "DELETE", "/api/dvswitch/private-node", map[string]any{"restart": true}, &out)
	if c == 200 {
		t.Fatal("expected apply_failed")
	}
	if c != 502 || !out.Rollback.Available {
		t.Fatalf("%d %+v", c, out)
	}
	pe.fail.Store(false)
	if c := pe.do("admin", "POST", "/api/dvswitch/private-node/rollback", map[string]any{}, nil); c != 200 {
		t.Fatalf("rollback %d", c)
	}
	if pe.file("rpt.conf") != rptBefore || pe.dv.priv == nil || *pe.dv.priv != recBefore || dvswitch.ReadLinkSettings(pe.st) != settingsBefore {
		t.Fatal("not restored")
	}
}

// ---- "Disconnect other links first" (dvswitch.link_exclusive)

func ilinkIndex(cmds []string, sub string) int {
	for i, c := range cmds {
		if strings.Contains(c, sub) {
			return i
		}
	}
	return -1
}

// Saving the setting alone is a settings write: no rpt.conf change, no
// restart; and it defaults to on, like the analog Connect.
func TestDVPrivateExclusiveSavedWithoutRestart(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, false)
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", map[string]any{"node": "1996", "localNode": "1997", "restart": true}, nil); c != 200 {
		t.Fatal(c)
	}
	if !dvswitch.ReadLinkSettings(pe.st).Exclusive {
		t.Fatal("default is not on")
	}
	pe.dh.SetFile("rpt.conf", pe.file("rpt.conf"))
	restarts := countCmd(pe.env, "core restart gracefully")
	body := map[string]any{"node": "1996", "localNode": "1997", "linkExclusive": false}
	var p api.DVPlan
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", map[string]any{"node": "1996", "localNode": "1997", "linkExclusive": false, "dryRun": true}, &p); c != 200 || p.RestartAsterisk || len(p.Restarts) != 0 || p.Link.Exclusive {
		t.Fatalf("%d %+v", c, p)
	}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, nil); c != 200 {
		t.Fatalf("save: %d", c)
	}
	if v, _ := pe.st.GetSetting(dvswitch.SettingLinkExclusive); v != "0" {
		t.Fatalf("setting = %q", v)
	}
	if countCmd(pe.env, "core restart gracefully") != restarts {
		t.Fatal("saving the setting restarted Asterisk")
	}
	// absent keeps the saved value
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", map[string]any{"node": "1996", "localNode": "1997"}, nil); c != 200 {
		t.Fatal(c)
	}
	if dvswitch.ReadLinkSettings(pe.st).Exclusive {
		t.Fatal("an absent linkExclusive reset the saved value")
	}
}

// A save in the panel (permanent mode, setting on) links the bridge node
// exclusively: the other links go first. The reconciler's own relinks never do.
func TestDVPrivateSavePermanentExclusiveConnect(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, true)
	pe.srv.LinkGoneWait = 20 * time.Millisecond
	pe.setLinks("50241", "1999")
	body := map[string]any{"node": "1996", "localNode": "1997", "linkMode": "permanent", "linkExclusive": true, "restart": true}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, nil); c != 200 {
		t.Fatal(c)
	}
	waitFor(t, func() bool { return ilink13(pe.env) >= 1 })
	cmds := pe.fake.ilinkCommands()
	a, b, c13 := ilinkIndex(cmds, "ilink 1 50241"), ilinkIndex(cmds, "ilink 1 1999"), ilinkIndex(cmds, "ilink 13 1996")
	if a < 0 || b < 0 || c13 < a || c13 < b {
		t.Fatalf("the other links were not disconnected before the link: %v", cmds)
	}
}

func TestDVPrivateSavePermanentNotExclusiveWhenOff(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, true)
	pe.setLinks("50241")
	body := map[string]any{"node": "1996", "localNode": "1997", "linkMode": "permanent", "linkExclusive": false, "restart": true}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, nil); c != 200 {
		t.Fatal(c)
	}
	waitFor(t, func() bool { return ilink13(pe.env) >= 1 })
	if cmds := pe.fake.ilinkCommands(); ilinkIndex(cmds, "ilink 1 50241") >= 0 {
		t.Fatalf("disconnected with the setting off: %v", cmds)
	}
}

// The reconciler's automatic relink (here: first check after start) never
// disconnects other links, even with the setting on.
func TestDVReconcilerAutoRelinkNeverExclusive(t *testing.T) {
	e := dvLinkEnv(t, true)
	e.setLinks("50241")
	if err := dvswitch.WriteLinkSettings(e.st, dvswitch.LinkSettings{Mode: "permanent", LocalNode: localNode, Bridge: "1996", Exclusive: true}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return ilink13(e) >= 1 })
	if cmds := e.fake.ilinkCommands(); ilinkIndex(cmds, "ilink 1 50241") >= 0 {
		t.Fatalf("automatic relink disconnected a link made by hand: %v", cmds)
	}
}

// After a renumber the monitor may still list the old bridge node, which is no
// longer in rpt.conf and never answers: the health check must not wait for it.
func TestDVPrivateRenumberHealthIgnoresOldNode(t *testing.T) {
	pe := newPrivEnv(t, nodesRpt, false)
	pe.set("1998", nil)
	pe.mon.SetNodes([]string{localNode, "1998"})
	pe.bad.Store("1998", true)
	var out struct {
		dvApplyOut
		Health struct{ Checks []struct{ Name string } }
	}
	start := time.Now()
	body := map[string]any{"node": "1999", "renumber": true, "restart": true}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node", body, &out); c != 200 || !out.Applied || out.Step != "" {
		t.Fatalf("%d %+v", c, out)
	}
	if d := time.Since(start); d >= 300*time.Millisecond {
		t.Errorf("apply took %v: it waited for the health timeout", d)
	}
	for _, c := range out.Health.Checks {
		if c.Name == "node 1998" {
			t.Errorf("the old node was checked: %+v", out.Health.Checks)
		}
	}
}

// PUT /api/dvswitch/link saves the setting alone (no apply, no restart) for
// operators; the bridge form keeps it when it does not send it.
func TestDVLinkExclusiveEndpoint(t *testing.T) {
	pe := newPrivEnv(t, tmplRpt, true)
	pe.set("1996", nil)
	restarts := countCmd(pe.env, "core restart gracefully")
	var out struct{ Exclusive bool }
	if c := pe.do("op", "PUT", "/api/dvswitch/link", map[string]any{"exclusive": false}, &out); c != 200 || out.Exclusive {
		t.Fatalf("%d %+v", c, out)
	}
	if v, _ := pe.st.GetSetting(dvswitch.SettingLinkExclusive); v != "0" {
		t.Fatalf("setting = %q", v)
	}
	if countCmd(pe.env, "core restart gracefully") != restarts {
		t.Fatal("restarted Asterisk")
	}
	// saving the bridge form without the field keeps it
	pe.set("1996", nil)
	if dvswitch.ReadLinkSettings(pe.st).Exclusive {
		t.Fatal("the form reset the setting")
	}
	if c := pe.do("op", "PUT", "/api/dvswitch/link", map[string]any{"exclusive": true}, nil); c != 200 {
		t.Fatal(c)
	}
	if !dvswitch.ReadLinkSettings(pe.st).Exclusive {
		t.Fatal("not saved")
	}
	expectErr(t, pe.env, "op", "PUT", "/api/dvswitch/link", map[string]any{}, 400, "bad_request")
	expectErr(t, pe.env, "viewer", "PUT", "/api/dvswitch/link", map[string]any{"exclusive": false}, 403, "forbidden")
}

// PUT /api/dvswitch/private-node/name saves a display name alone (admin, no
// restart); the snapshot carries it, with the default when none is saved.
func TestDVNodeNameEndpoint(t *testing.T) {
	pe := &privEnv{env: dvLinkEnv(t, false)}
	pe.st = pe.env.st
	restarts := countCmd(pe.env, "core restart gracefully")
	var out struct{ Name string }
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node/name", map[string]any{"name": "  Example room  "}, &out); c != 200 || out.Name != "Example room" {
		t.Fatalf("%d %+v", c, out)
	}
	if v, _ := pe.st.GetSetting(dvswitch.SettingNodeName); v != "Example room" {
		t.Fatalf("setting = %q", v)
	}
	if countCmd(pe.env, "core restart gracefully") != restarts {
		t.Fatal("restarted Asterisk")
	}
	var sn api.DVSnapshot
	if c := pe.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 || sn.BridgeNode.Name != "Example room" {
		t.Fatalf("%d %+v", c, sn.BridgeNode)
	}
	if c := pe.do("admin", "PUT", "/api/dvswitch/private-node/name", map[string]any{"name": ""}, &out); c != 200 || out.Name != dvswitch.DefaultNodeName {
		t.Fatalf("%d %+v", c, out)
	}
	if c := pe.do("viewer", "GET", "/api/dvswitch/snapshot", nil, &sn); c != 200 || sn.BridgeNode.Name != dvswitch.DefaultNodeName {
		t.Fatalf("%d %+v", c, sn.BridgeNode)
	}
	expectErr(t, pe.env, "admin", "PUT", "/api/dvswitch/private-node/name", map[string]any{}, 400, "bad_request")
	expectErr(t, pe.env, "admin", "PUT", "/api/dvswitch/private-node/name", map[string]any{"name": strings.Repeat("x", 41)}, 400, "bad_request")
	expectErr(t, pe.env, "op", "PUT", "/api/dvswitch/private-node/name", map[string]any{"name": "x"}, 403, "forbidden")
}
