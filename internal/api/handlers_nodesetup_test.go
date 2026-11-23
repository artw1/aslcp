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
