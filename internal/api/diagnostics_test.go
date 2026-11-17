package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/config"
	"github.com/artw1/aslcp/internal/diag"
)

func TestAbout(t *testing.T) {
	e := newEnv(t, opts{})
	expectErr(t, e, "", "GET", "/api/about", nil, 401, "unauthorized")
	for _, role := range []string{"admin", "op", "viewer"} {
		var a struct {
			Version  string `json:"version"`
			Asterisk string `json:"asterisk"`
			Platform struct {
				OS       string `json:"os"`
				Arch     string `json:"arch"`
				Hardware string `json:"hardware"`
			} `json:"platform"`
		}
		if c := e.do(role, "GET", "/api/about", nil, &a); c != 200 {
			t.Fatalf("%s: %d", role, c)
		}
		if a.Version != "test" || a.Platform.OS == "" || a.Platform.Arch == "" || a.Platform.Hardware == "" {
			t.Fatalf("%s: %+v", role, a)
		}
	}
}

func TestDiagnostics(t *testing.T) {
	ring := diag.NewRing(50)
	var secret string
	e := newEnv(t, opts{log: ring, cfg: func(s string) config.Config {
		secret = s
		c := config.Defaults()
		c.AMISecret = s
		c.NodelistRefresh = time.Hour
		return c
	}})
	ring.Write([]byte("2026/11/15 ami login with secret=" + secret + " from 203.0.113.50\nplain mention " + secret + " here, peer K1ZZZ mail x@y.org\n"))
	e.srv.AddSecret("late-resolved-secret")
	ring.Write([]byte("oops late-resolved-secret\n"))

	for _, role := range []string{"op", "viewer"} {
		expectErr(t, e, role, "GET", "/api/diagnostics", nil, 403, "forbidden")
	}
	expectErr(t, e, "", "GET", "/api/diagnostics", nil, 401, "unauthorized")

	code, body, hdr := e.raw("GET", "/api/diagnostics", nil, e.ck["admin"], nil)
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	if ct := hdr.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
		t.Errorf("content-type %q", ct)
	}
	if cd := hdr.Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="aslcp-diagnostics-`) || !strings.HasSuffix(cd, `.txt"`) {
		t.Errorf("disposition %q", cd)
	}
	out := string(body)
	for _, bad := range []string{secret, "late-resolved-secret", "203.0.113.50", "K1ZZZ", "x@y.org"} {
		if secret == "" || strings.Contains(out, bad) {
			t.Errorf("diagnostics contains %q", bad)
		}
	}
	for _, good := range []string{"ASLCP:    test", "Node " + localNode, "N0AAD", "Connected: yes", "Entries: 4", "== Server log", "<redacted>", "<ipv4>", "[ami]"} {
		if !strings.Contains(out, good) {
			t.Errorf("diagnostics lacks %q\n%s", good, out)
		}
	}
}
