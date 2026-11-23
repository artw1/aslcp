package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/rpt"
)

func setupEnvAPI(t *testing.T) *env {
	t.Helper()
	return newEnv(t, opts{sysHost: helper.NewDemoSystem(), configFn: func(c rpt.API) *configsvc.Service {
		s := configsvc.New(configsvc.NewDemoHelper(), c.(configsvc.AMI), rpt.New(c))
		s.HealthTimeout, s.HealthPoll = 2*time.Second, 20*time.Millisecond
		return s
	}})
}

func setupReq() map[string]any {
	return map[string]any{"oldNode": "2006", "node": "1999", "password": "example-pass", "callsign": "N0CALL",
		"radio": "simpleusb", "preset": "simplex", "ami": true}
}

func TestSetupAuthz(t *testing.T) {
	e := setupEnvAPI(t)
	for _, role := range []string{"op", "viewer"} {
		for _, r := range [][2]string{{"GET", "/api/config/setup/skip"}, {"PUT", "/api/config/setup/skip"},
			{"POST", "/api/config/setup/plan"}, {"POST", "/api/config/setup/apply"}, {"POST", "/api/config/setup/rollback"}} {
			expectErr(t, e, role, r[0], r[1], map[string]any{}, 403, "forbidden")
		}
	}
}

func TestSetupSkipFlag(t *testing.T) {
	e := setupEnvAPI(t)
	var v struct{ Skipped bool }
	if c := e.do("admin", "GET", "/api/config/setup/skip", nil, &v); c != 200 || v.Skipped {
		t.Fatalf("initial: %d %+v", c, v)
	}
	if c := e.do("admin", "PUT", "/api/config/setup/skip", map[string]any{"skipped": true}, &v); c != 200 || !v.Skipped {
		t.Fatalf("set: %d %+v", c, v)
	}
	v.Skipped = false
	if c := e.do("admin", "GET", "/api/config/setup/skip", nil, &v); c != 200 || !v.Skipped {
		t.Fatalf("get: %d %+v", c, v)
	}
	e.do("admin", "PUT", "/api/config/setup/skip", map[string]any{"skipped": false}, &v)
	if v.Skipped {
		t.Fatal("restart did not clear the flag")
	}
}

func TestSetupPlanApplyRollback(t *testing.T) {
	e := setupEnvAPI(t)
	var raw json.RawMessage
	if c := e.do("admin", "POST", "/api/config/setup/plan", setupReq(), &raw); c != 200 {
		t.Fatalf("plan: %d %s", c, raw)
	}
	var pl struct {
		Plan struct {
			Node     string
			Restart  bool
			Renumber bool
		}
		AMIToken string
	}
	if err := json.Unmarshal(raw, &pl); err != nil || pl.AMIToken == "" || pl.Plan.Node != "1999" || !pl.Plan.Renumber {
		t.Fatalf("plan body: %v %s", err, raw)
	}
	body := setupReq()
	body["amiToken"] = pl.AMIToken
	raw = nil
	if c := e.do("admin", "POST", "/api/config/setup/apply", body, &raw); c != 200 {
		t.Fatalf("apply: %d %s", c, raw)
	}
	if strings.Contains(string(raw), "example-pass") {
		t.Fatalf("apply leaks the node password: %s", raw)
	}
	var ap struct {
		Steps    []struct{ ID, Status string }
		Rollback struct{ Commit, Node, OldNode string }
	}
	if err := json.Unmarshal(raw, &ap); err != nil || len(ap.Steps) == 0 || ap.Rollback.Commit == "" {
		t.Fatalf("apply body: %v %s", err, raw)
	}
	for _, s := range ap.Steps {
		if s.Status == "failed" {
			t.Fatalf("step failed: %s", raw)
		}
	}
	// the token is single use
	expectErr(t, e, "admin", "POST", "/api/config/setup/apply", body, 400, "bad_request")
	au, _ := e.st.ListAudit(50)
	found := false
	for _, a := range au {
		if strings.Contains(a.Detail, "example-pass") {
			t.Fatalf("audit leaks: %+v", a)
		}
		if a.Action == "config.setup" {
			found = true
		}
	}
	if !found {
		t.Fatal("no config.setup audit entry")
	}
	rb := map[string]any{"commit": ap.Rollback.Commit, "node": ap.Rollback.Node, "oldNode": ap.Rollback.OldNode}
	raw = nil
	if c := e.do("admin", "POST", "/api/config/setup/rollback", rb, &raw); c != 200 {
		t.Fatalf("rollback: %d %s", c, raw)
	}
}

func TestSetupRefusals(t *testing.T) {
	e := setupEnvAPI(t)
	bad := setupReq()
	bad["node"] = "x1"
	expectErr(t, e, "admin", "POST", "/api/config/setup/plan", bad, 400, "bad_request")
	bad = setupReq()
	bad["oldNode"] = "1234"
	var raw json.RawMessage
	if c := e.do("admin", "POST", "/api/config/setup/plan", bad, &raw); c < 400 || c >= 500 {
		t.Fatalf("unknown old node: %d %s", c, raw)
	}
	// a stale base is a conflict
	body := setupReq()
	body["amiToken"] = ""
	body["ami"] = false
	body["base"] = map[string]string{"rpt.conf": "deadbeef"}
	expectErr(t, e, "admin", "POST", "/api/config/setup/apply", body, 409, "conflict")
}

func TestWizardAuthz(t *testing.T) {
	e := setupEnvAPI(t)
	for _, role := range []string{"op", "viewer"} {
		for _, p := range []string{"/api/config/wizard/add-node/plan", "/api/config/wizard/add-node/apply",
			"/api/config/wizard/renumber/plan", "/api/config/wizard/renumber/apply"} {
			expectErr(t, e, role, "POST", p, map[string]any{}, 403, "forbidden")
		}
	}
}

func TestAddNodeWizard(t *testing.T) {
	e := setupEnvAPI(t)
	body := map[string]any{"node": "1998", "password": "example-pass", "callsign": "N0CALL", "radio": "hub", "preset": "hub"}
	var raw json.RawMessage
	if c := e.do("admin", "POST", "/api/config/wizard/add-node/plan", body, &raw); c != 200 {
		t.Fatalf("plan: %d %s", c, raw)
	}
	var pl struct {
		Plan struct {
			Node    string
			Restart bool
			Base    map[string]string
			Steps   []struct{ ID, Skipped string }
		}
	}
	if err := json.Unmarshal(raw, &pl); err != nil || pl.Plan.Node != "1998" || !pl.Plan.Restart || len(pl.Plan.Steps) == 0 {
		t.Fatalf("plan body: %v %s", err, raw)
	}
	if strings.Contains(string(raw), "example-pass") {
		t.Fatalf("plan leaks the password: %s", raw)
	}
	// an existing node is refused
	dup := map[string]any{"node": "2006", "password": "example-pass", "callsign": "N0CALL", "radio": "hub", "preset": "hub"}
	expectErr(t, e, "admin", "POST", "/api/config/wizard/add-node/plan", dup, 400, "bad_request")
	// a stale base is a conflict
	stale := map[string]any{"node": "1998", "password": "example-pass", "callsign": "N0CALL", "radio": "hub", "preset": "hub", "base": map[string]string{"rpt.conf": "deadbeef"}}
	expectErr(t, e, "admin", "POST", "/api/config/wizard/add-node/apply", stale, 409, "conflict")
	body["base"] = pl.Plan.Base
	raw = nil
	if c := e.do("admin", "POST", "/api/config/wizard/add-node/apply", body, &raw); c != 200 {
		t.Fatalf("apply: %d %s", c, raw)
	}
	var ap struct {
		Steps    []struct{ ID, Status string }
		Rollback struct {
			Commit, Node, OldNode string
			Added                 bool
		}
	}
	if err := json.Unmarshal(raw, &ap); err != nil || ap.Rollback.Commit == "" || ap.Rollback.Node != "1998" || !ap.Rollback.Added {
		t.Fatalf("apply body: %v %s", err, raw)
	}
	for _, s := range ap.Steps {
		if s.Status == "failed" {
			t.Fatalf("step failed: %s", raw)
		}
	}
	// the node is in the list now, and the second add is refused
	var ov struct{ Nodes []struct{ Node string } }
	e.do("admin", "GET", "/api/config/nodes", nil, &ov)
	found := false
	for _, n := range ov.Nodes {
		found = found || n.Node == "1998"
	}
	if !found {
		t.Fatalf("node 1998 not listed: %+v", ov)
	}
	expectErr(t, e, "admin", "POST", "/api/config/wizard/add-node/plan", body, 400, "bad_request")
	au, _ := e.st.ListAudit(50)
	ok := false
	for _, a := range au {
		if strings.Contains(a.Detail, "example-pass") {
			t.Fatalf("audit leaks: %+v", a)
		}
		ok = ok || a.Action == "config.addnode"
	}
	if !ok {
		t.Fatal("no config.addnode audit entry")
	}
	// rollback restores the files
	rb := map[string]any{"commit": ap.Rollback.Commit, "node": ap.Rollback.Node, "oldNode": ap.Rollback.OldNode, "restart": true, "added": ap.Rollback.Added}
	raw = nil
	if c := e.do("admin", "POST", "/api/config/setup/rollback", rb, &raw); c != 200 {
		t.Fatalf("rollback: %d %s", c, raw)
	}
	ov.Nodes = nil
	e.do("admin", "GET", "/api/config/nodes", nil, &ov)
	for _, n := range ov.Nodes {
		if n.Node == "1998" {
			t.Fatal("rollback left node 1998")
		}
	}
}

func TestRenumberWizard(t *testing.T) {
	e := setupEnvAPI(t)
	body := map[string]any{"oldNode": "2006", "node": "1998", "savenode": true, "password": "example-pass"}
	var raw json.RawMessage
	if c := e.do("admin", "POST", "/api/config/wizard/renumber/plan", body, &raw); c != 200 {
		t.Fatalf("plan: %d %s", c, raw)
	}
	var pl struct {
		Plan struct {
			Renumber bool
			Manual   []string
			Base     map[string]string
		}
	}
	if err := json.Unmarshal(raw, &pl); err != nil || !pl.Plan.Renumber || len(pl.Plan.Manual) < 4 {
		t.Fatalf("plan body: %v %s", err, raw)
	}
	expectErr(t, e, "admin", "POST", "/api/config/wizard/renumber/plan", map[string]any{"oldNode": "2006", "node": "2006"}, 400, "bad_request")
	expectErr(t, e, "admin", "POST", "/api/config/wizard/renumber/plan", map[string]any{"oldNode": "2006", "node": "1998", "savenode": true}, 400, "bad_request")
	body["base"] = pl.Plan.Base
	raw = nil
	if c := e.do("admin", "POST", "/api/config/wizard/renumber/apply", body, &raw); c != 200 {
		t.Fatalf("apply: %d %s", c, raw)
	}
	if strings.Contains(string(raw), "example-pass") {
		t.Fatalf("apply leaks the password: %s", raw)
	}
	var ap struct {
		Steps    []struct{ ID, Status string }
		Rollback struct{ Commit, Node, OldNode string }
	}
	if err := json.Unmarshal(raw, &ap); err != nil || ap.Rollback.Commit == "" || ap.Rollback.OldNode != "2006" {
		t.Fatalf("apply body: %v %s", err, raw)
	}
	for _, s := range ap.Steps {
		if s.Status == "failed" {
			t.Fatalf("step failed: %s", raw)
		}
	}
	rb := map[string]any{"commit": ap.Rollback.Commit, "node": ap.Rollback.Node, "oldNode": ap.Rollback.OldNode, "restart": true}
	raw = nil
	if c := e.do("admin", "POST", "/api/config/setup/rollback", rb, &raw); c != 200 {
		t.Fatalf("rollback: %d %s", c, raw)
	}
}

func TestFormAMISecretFollow(t *testing.T) {
	e := setupEnvAPI(t)
	calls := 0
	e.srv.SetAMILoginReloader(func(bool) (bool, error) { calls++; return true, nil })
	body := map[string]any{"node": "2006", "changes": []map[string]string{{"id": "manager.conf:admin:secret", "value": "example-pass"}}}
	var raw json.RawMessage
	if c := e.do("admin", "POST", "/api/config/form", body, &raw); c != 200 {
		t.Fatalf("form: %d %s", c, raw)
	}
	if calls != 1 {
		t.Fatalf("the AMI login was reloaded %d times", calls)
	}
	if strings.Contains(string(raw), "example-pass") {
		t.Fatalf("response leaks the secret: %s", raw)
	}
	var out struct{ Notices []string }
	json.Unmarshal(raw, &out)
	if len(out.Notices) != 1 || !strings.Contains(out.Notices[0], "now uses the new one") {
		t.Fatalf("notices: %v", out.Notices)
	}
	// a literal secret in aslcp.conf: no follow, a notice says so
	e.srv.SetAMILoginReloader(func(bool) (bool, error) { return false, nil })
	body["changes"] = []map[string]string{{"id": "manager.conf:admin:secret", "value": "example-pass2"}}
	raw = nil
	e.do("admin", "POST", "/api/config/form", body, &raw)
	out.Notices = nil
	json.Unmarshal(raw, &out)
	if len(out.Notices) != 1 || !strings.Contains(out.Notices[0], "aslcp.conf") {
		t.Fatalf("notices: %v", out.Notices)
	}
}
