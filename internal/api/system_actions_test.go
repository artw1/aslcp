package api_test

import "testing"

func TestNodeStats(t *testing.T) {
	e := newEnv(t, opts{})
	for _, role := range []string{"admin", "op", "viewer"} {
		var out struct {
			Node  string `json:"node"`
			Stats []struct{ Key, Value string }
		}
		if c := e.do(role, "GET", "/api/nodes/"+localNode+"/stats", nil, &out); c != 200 {
			t.Fatalf("%s: %d", role, c)
		}
		if out.Node != localNode || len(out.Stats) != 3 || out.Stats[1].Key != "KeyupsToday" || out.Stats[1].Value != "2" {
			t.Fatalf("%+v", out)
		}
	}
	expectErr(t, e, "viewer", "GET", "/api/nodes/99999/stats", nil, 404, "unknown_node")
}

func TestRestartAsterisk(t *testing.T) {
	e := newEnv(t, opts{})
	for _, role := range []string{"op", "viewer"} {
		expectErr(t, e, role, "POST", "/api/system/restart-asterisk", nil, 403, "forbidden")
	}
	if got := countCmd(e, "core restart gracefully"); got != 0 {
		t.Fatalf("restart sent before request: %d", got)
	}
	if c := e.do("admin", "POST", "/api/system/restart-asterisk", nil, nil); c != 202 {
		t.Fatalf("admin: %d", c)
	}
	if got := countCmd(e, "core restart gracefully"); got != 1 {
		t.Fatalf("commands sent: %d", got)
	}
}

func countCmd(e *env, cmd string) int {
	e.fake.mu.Lock()
	defer e.fake.mu.Unlock()
	n := 0
	for _, c := range e.fake.commands {
		if c == cmd {
			n++
		}
	}
	return n
}
