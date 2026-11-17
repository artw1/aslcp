package api_test

import (
	"strings"
	"testing"
	"time"
)

// setLinks changes the fake node's links and waits until the monitor sees them.
func (e *env) setLinks(links ...string) {
	e.t.Helper()
	e.fake.mu.Lock()
	e.fake.links = links
	e.fake.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := e.mon.Status(localNode); ok && !s.Stale && len(s.Links) == len(links) {
			match := true
			for i, l := range s.Links {
				if l.Node != links[i] {
					match = false
				}
			}
			if match {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("monitor never showed links %v", links)
}

func (e *env) connectCmds(body map[string]any) []string {
	e.t.Helper()
	before := len(e.fake.ilinkCommands())
	if c := e.do("op", "POST", "/api/nodes/"+localNode+"/connect", body, nil); c != 202 {
		e.t.Fatalf("connect %v: %d", body, c)
	}
	return e.fake.ilinkCommands()[before:]
}

func TestConnectExclusive(t *testing.T) {
	e := newEnv(t, opts{})
	e.setLinks("50241", "1999", "W1AW")

	got := e.connectCmds(map[string]any{"target": "2000", "exclusive": true})
	want := []string{
		"rpt cmd 1997 ilink 1 50241", "rpt cmd 1997 ilink 1 1999", "rpt cmd 1997 ilink 1 W1AW",
		"rpt cmd 1997 ilink 3 2000",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("exclusive transceive:\n%s", strings.Join(got, "\n"))
	}

	got = e.connectCmds(map[string]any{"target": "2000", "mode": "monitor", "exclusive": true, "permanent": true})
	want = []string{
		"rpt cmd 1997 ilink 1 50241", "rpt cmd 1997 ilink 1 1999", "rpt cmd 1997 ilink 1 W1AW",
		"rpt cmd 1997 ilink 12 2000",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("exclusive monitor:\n%s", strings.Join(got, "\n"))
	}

	// not exclusive: only the connect
	got = e.connectCmds(map[string]any{"target": "2000", "exclusive": false})
	if len(got) != 1 || got[0] != "rpt cmd 1997 ilink 3 2000" {
		t.Fatalf("exclusive=false: %v", got)
	}
	got = e.connectCmds(map[string]any{"target": "2000"})
	if len(got) != 1 {
		t.Fatalf("exclusive absent: %v", got)
	}

	// localmonitor ignores exclusive
	got = e.connectCmds(map[string]any{"target": "2000", "mode": "localmonitor", "exclusive": true})
	if len(got) != 1 || got[0] != "rpt cmd 1997 ilink 8 2000" {
		t.Fatalf("localmonitor: %v", got)
	}
}

func TestConnectExclusiveTargetAlreadyLinked(t *testing.T) {
	e := newEnv(t, opts{})
	e.setLinks("50241", "2000")
	got := e.connectCmds(map[string]any{"target": "2000", "exclusive": true})
	want := []string{"rpt cmd 1997 ilink 1 50241", "rpt cmd 1997 ilink 3 2000"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got:\n%s", strings.Join(got, "\n"))
	}
	e.setLinks("2000")
	got = e.connectCmds(map[string]any{"target": "2000", "exclusive": true})
	if len(got) != 1 || got[0] != "rpt cmd 1997 ilink 3 2000" {
		t.Fatalf("only target linked: %v", got)
	}
}

func TestConnectExclusiveNoLinks(t *testing.T) {
	e := newEnv(t, opts{})
	e.setLinks()
	got := e.connectCmds(map[string]any{"target": "2000", "exclusive": true})
	if len(got) != 1 || got[0] != "rpt cmd 1997 ilink 3 2000" {
		t.Fatalf("got: %v", got)
	}
}

func TestSettings(t *testing.T) {
	e := newEnv(t, opts{})
	var s struct {
		FavoritesScan bool `json:"favoritesScan"`
	}
	for _, role := range []string{"admin", "op", "viewer"} {
		if c := e.do(role, "GET", "/api/settings", nil, &s); c != 200 || s.FavoritesScan {
			t.Fatalf("%s GET: %d %+v", role, c, s)
		}
	}
	// The connect defaults are no longer a server setting.
	var raw map[string]any
	e.do("viewer", "GET", "/api/settings", nil, &raw)
	if _, ok := raw["exclusiveDefault"]; ok {
		t.Fatalf("exclusiveDefault still served: %v", raw)
	}
	if c := e.do("admin", "PUT", "/api/settings", map[string]any{"exclusiveDefault": false}, nil); c != 404 && c != 405 {
		t.Fatalf("PUT /api/settings: %d (want removed)", c)
	}
}
