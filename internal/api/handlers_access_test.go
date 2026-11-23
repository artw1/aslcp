package api_test

import (
	"encoding/json"
	"testing"
)

type accessOut struct {
	Node     string
	Mode     string
	Enforced bool
	Limit    int
	Allow    []struct{ Remote, Note string }
	Deny     []struct{ Remote, Note string }
}

func TestAccessLists(t *testing.T) {
	e := setupEnvAPI(t)
	var v accessOut
	if c := e.do("admin", "GET", "/api/config/access?node=2006", nil, &v); c != 200 || v.Mode != "open" || !v.Enforced {
		t.Fatalf("initial: %d %+v", c, v)
	}
	add := func(fam, remote, note string) (int, accessOut) {
		var out accessOut
		var raw json.RawMessage
		c := e.do("admin", "POST", "/api/config/access", map[string]any{"node": "2006", "op": "add", "family": fam, "remote": remote, "note": note}, &raw)
		json.Unmarshal(raw, &out)
		return c, out
	}
	if c, out := add("denylist", "1999", "test hub"); c != 200 || out.Mode != "deny" || len(out.Deny) != 1 || out.Deny[0].Note != "test hub" {
		t.Fatalf("add deny: %d %+v", c, out)
	}
	if c, out := add("allowlist", "2001", ""); c != 200 || out.Mode != "allow" || len(out.Allow) != 1 || out.Allow[0].Note != "" {
		t.Fatalf("add allow: %d %+v", c, out)
	}
	// duplicates, bad values and other nodes are refused
	for _, b := range []map[string]any{
		{"node": "2006", "op": "add", "family": "allowlist", "remote": "2001"},
		{"node": "2006", "op": "add", "family": "allowlist", "remote": "20x1"},
		{"node": "2006", "op": "add", "family": "allowlist", "remote": "2001/../x"},
		{"node": "2006", "op": "add", "family": "favorites", "remote": "2002"},
		{"node": "2006", "op": "add", "family": "allowlist", "remote": "2002", "note": "a;b"},
		{"node": "2006", "op": "add", "family": "allowlist", "remote": "2006"},
		{"node": "4242", "op": "add", "family": "allowlist", "remote": "2002"},
		{"node": "2006", "op": "remove", "family": "allowlist", "remote": "2002"},
		{"node": "2006", "op": "zap", "family": "allowlist", "remote": "2002"},
	} {
		expectErr(t, e, "admin", "POST", "/api/config/access", b, 400, "bad_request")
	}
	var out accessOut
	if c := e.do("admin", "POST", "/api/config/access", map[string]any{"node": "2006", "op": "remove", "family": "allowlist", "remote": "2001"}, &out); c != 200 || out.Mode != "deny" {
		t.Fatalf("remove allow: %d %+v", c, out)
	}
	if c := e.do("admin", "POST", "/api/config/access", map[string]any{"node": "2006", "op": "remove", "family": "denylist", "remote": "1999"}, &out); c != 200 || out.Mode != "open" {
		t.Fatalf("remove deny: %d %+v", c, out)
	}
	for _, role := range []string{"op", "viewer"} {
		expectErr(t, e, role, "GET", "/api/config/access?node=2006", nil, 403, "forbidden")
		expectErr(t, e, role, "POST", "/api/config/access", map[string]any{}, 403, "forbidden")
	}
	expectErr(t, e, "admin", "GET", "/api/config/access?node=4242", nil, 400, "bad_request")
	// the writes reached only the two families
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	if len(e.fake.db) != 0 {
		t.Fatalf("database left over: %v", e.fake.db)
	}
}

func TestDTMFEndpoint(t *testing.T) {
	e := setupEnvAPI(t)
	var v struct {
		Node   string
		Groups []struct {
			Kind, Stanza, SameAs string
			Items                []struct {
				Code, Action, Desc, File string
				Line                     int
			}
		}
	}
	if c := e.do("admin", "GET", "/api/config/dtmf?node=2006", nil, &v); c != 200 || len(v.Groups) != 5 {
		t.Fatalf("dtmf: %d %+v", c, v)
	}
	var fn, mac int
	for _, g := range v.Groups {
		switch g.Kind {
		case "functions":
			for _, it := range g.Items {
				if it.Code == "70" && (it.Desc != "System status" || it.File != "rpt.conf" || it.Line == 0) {
					t.Fatalf("code 70: %+v", it)
				}
				if it.Code == "987" && it.Desc != "" {
					t.Fatalf("unknown code described: %+v", it)
				}
			}
			fn = len(g.Items)
		case "link_functions":
			if g.SameAs != "functions" {
				t.Fatalf("link_functions: %+v", g)
			}
		case "macro":
			mac = len(g.Items)
			if g.Stanza != "macro-demo" || g.Items[0].File != "custom/rpt/macros.conf" {
				t.Fatalf("macro: %+v", g)
			}
		}
	}
	if fn != 8 || mac != 1 {
		t.Fatalf("items: functions %d macro %d", fn, mac)
	}
	expectErr(t, e, "admin", "GET", "/api/config/dtmf?node=4242", nil, 422, "refused")
	for _, role := range []string{"op", "viewer"} {
		expectErr(t, e, role, "GET", "/api/config/dtmf?node=2006", nil, 403, "forbidden")
	}
}
