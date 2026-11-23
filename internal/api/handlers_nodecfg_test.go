package api_test

import (
	"encoding/json"
	"strings"
	"testing"
)

type cfgNode struct {
	Node        string
	Private     bool
	Placeholder bool
	Callsign    string
	Stanza      struct{ Template string }
	Radio       struct {
		Type      string
		Supported bool
	}
	Fields []struct {
		ID, Key, Value string
		Source         struct{ Kind, Section string }
		CanReset       bool
	}
	Registration struct{ Method string }
	Refusals     []struct{ Code string }
}

type cfgOverview struct {
	Nodes []cfgNode
	Base  map[string]string
}

type cfgPlan struct {
	Node    string
	Mode    string
	Noop    bool
	Restart bool
	Files   []struct {
		Path, Diff string
		Changed    bool
	}
	Base     map[string]string
	Errors   []struct{ Code string }
	Warnings []struct{ Code string }
	Changes  []struct{ ID, Action, To string }
}

type cfgFormRes struct {
	Plan           cfgPlan
	Result         *struct{ Commit string }
	Restarted      bool
	RestartPending bool
}

func TestNodeCfgAuthz(t *testing.T) {
	e := withConfig(t)
	for _, role := range []string{"op", "viewer"} {
		for _, r := range [][2]string{{"GET", "/api/config/nodes"}, {"GET", "/api/config/schema"}, {"POST", "/api/config/form"}} {
			expectErr(t, e, role, r[0], r[1], map[string]any{}, 403, "forbidden")
		}
	}
	expectErr(t, newEnv(t, opts{}), "admin", "GET", "/api/config/nodes", nil, 503, "config_unavailable")
}

func TestNodeCfgOverviewAndSchema(t *testing.T) {
	e := withConfig(t)
	var ov cfgOverview
	if c := e.do("admin", "GET", "/api/config/nodes", nil, &ov); c != 200 || len(ov.Nodes) != 1 {
		t.Fatalf("nodes: %d %+v", c, ov)
	}
	n := ov.Nodes[0]
	if n.Node != "2006" || n.Private || n.Callsign != "N0CALL" || n.Stanza.Template != "node-main" || n.Radio.Type != "simpleusb" || !n.Radio.Supported || n.Registration.Method != "http" {
		t.Fatalf("node: %+v", n)
	}
	if ov.Base["rpt.conf"] == "" {
		t.Fatalf("base: %+v", ov.Base)
	}
	var sc struct {
		Keys    []struct{ ID, Reload string }
		Forms   []string
		Presets []struct{ Name string }
	}
	if c := e.do("admin", "GET", "/api/config/schema", nil, &sc); c != 200 || len(sc.Keys) < 30 || len(sc.Forms) == 0 || len(sc.Presets) != 4 {
		t.Fatalf("schema: %d keys=%d", c, len(sc.Keys))
	}
	for _, k := range sc.Keys {
		if k.Reload == "verified" {
			t.Fatalf("nothing is verified on hardware yet: %+v", k)
		}
	}
}

func TestNodeCfgFormDryRunApply(t *testing.T) {
	e := withConfig(t)
	body := map[string]any{"node": "2006", "dryRun": true,
		"changes": []map[string]string{{"id": "rpt.conf:node:hangtime", "value": "3000"}}}
	var dry cfgFormRes
	if c := e.do("admin", "POST", "/api/config/form", body, &dry); c != 200 || dry.Plan.Noop || len(dry.Plan.Files) != 1 || dry.Plan.Base["rpt.conf"] == "" || dry.Result != nil {
		t.Fatalf("dry run: %d %+v", c, dry)
	}
	if !strings.Contains(dry.Plan.Files[0].Diff, "hangtime") {
		t.Fatalf("diff: %s", dry.Plan.Files[0].Diff)
	}
	if n := countCmd(e, "module reload app_rpt.so"); n != 0 {
		t.Fatal("dry run reloaded")
	}
	body["dryRun"], body["base"] = false, dry.Plan.Base
	var res cfgFormRes
	if c := e.do("admin", "POST", "/api/config/form", body, &res); c != 200 || res.Result == nil || res.Result.Commit == "" || res.RestartPending {
		t.Fatalf("apply: %d %+v", c, res)
	}
	if countCmd(e, "module reload app_rpt.so") != 1 {
		t.Fatal("reload not sent")
	}
	var ov cfgOverview
	e.do("admin", "GET", "/api/config/nodes", nil, &ov)
	found := false
	for _, f := range ov.Nodes[0].Fields {
		if f.Key == "hangtime" {
			found = f.Value == "3000" && f.Source.Kind == "node" && f.CanReset
		}
	}
	if !found {
		t.Fatal("hangtime not saved as a node override")
	}
	// a plan based on the old file: it changed since
	body["changes"] = []map[string]string{{"id": "rpt.conf:node:hangtime", "value": "3500"}}
	var cf struct {
		Code  string
		Files []string
	}
	if c := e.do("admin", "POST", "/api/config/form", body, &cf); c != 409 || cf.Code != "conflict" || len(cf.Files) != 1 {
		t.Fatalf("conflict: %d %+v", c, cf)
	}
	// reset puts the template value back
	rb := map[string]any{"node": "2006", "mode": "reset", "changes": []map[string]string{{"id": "rpt.conf:node:hangtime"}}}
	var rr cfgFormRes
	if c := e.do("admin", "POST", "/api/config/form", rb, &rr); c != 200 || rr.Plan.Noop {
		t.Fatalf("reset: %d %+v", c, rr)
	}
	// nothing left to do
	var rn cfgFormRes
	if c := e.do("admin", "POST", "/api/config/form", rb, &rn); c != 200 || !rn.Plan.Noop || rn.Result != nil {
		t.Fatalf("noop: %d %+v", c, rn)
	}
	au, err := e.st.ListAudit(50)
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, a := range au {
		if a.Action == "config.form" {
			seen = true
			if strings.Contains(a.Detail, "3000") {
				t.Fatalf("audit has a value: %+v", a)
			}
		}
	}
	if !seen {
		t.Fatal("no config.form audit")
	}
}

func TestNodeCfgFormErrors(t *testing.T) {
	e := withConfig(t)
	expectErr(t, e, "admin", "POST", "/api/config/form", map[string]any{"node": "2006",
		"changes": []map[string]string{{"id": "rpt.conf:node:duplex", "value": "9"}}}, 400, "bad_request")
	expectErr(t, e, "admin", "POST", "/api/config/form", map[string]any{"node": "1997"}, 400, "bad_request")
	// the driver is off: the change is blocked, not applied
	expectErr(t, e, "admin", "POST", "/api/config/form", map[string]any{"node": "2006",
		"changes": []map[string]string{{"id": "rpt.conf:node:rxchannel", "value": "Radio/usb"}}}, 422, "blocked")
	// a node without a stanza of its own device: refused with the file to open in Advanced
	var rf struct {
		Code    string
		Refusal struct{ File string }
	}
	if c := e.do("admin", "POST", "/api/config/form", map[string]any{"node": "2006", "mode": "template",
		"changes": []map[string]string{{"id": "manager.conf:admin:secret", "value": "example-pass"}}}, &rf); c != 400 {
		t.Fatalf("template of a shared key: %d", c)
	}
}

func TestNodeCfgSecretsNeverEchoed(t *testing.T) {
	e := withConfig(t)
	body := map[string]any{"node": "2006", "dryRun": true, "link": true,
		"changes": []map[string]string{{"id": "echolink.conf:el0:call", "value": "N0CALL-L"}, {"id": "echolink.conf:el0:pwd", "value": "example-pass"}}}
	var raw json.RawMessage
	if c := e.do("admin", "POST", "/api/config/form", body, &raw); c != 200 || strings.Contains(string(raw), "example-pass") {
		t.Fatalf("dry run: %d %s", c, raw)
	}
	body["dryRun"], body["restart"] = false, true
	if c := e.do("admin", "POST", "/api/config/form", body, &raw); c != 200 || strings.Contains(string(raw), "example-pass") {
		t.Fatalf("apply: %d %s", c, raw)
	}
	var res cfgFormRes
	_ = json.Unmarshal(raw, &res)
	if !res.Restarted || res.RestartPending {
		t.Fatalf("restart: %+v", res)
	}
	au, _ := e.st.ListAudit(50)
	for _, a := range au {
		if strings.Contains(a.Detail, "example-pass") || strings.Contains(a.Detail, "N0CALL-L") {
			t.Fatalf("audit leaks: %+v", a)
		}
	}
}
