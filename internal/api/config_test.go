package api_test

import (
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/rpt"
)

func withConfig(t *testing.T) *env {
	t.Helper()
	// The service needs the env's AMI client, which newEnv builds; build it
	// in two steps through the Deps hook.
	return newEnv(t, opts{configFn: func(c rpt.API) *configsvc.Service {
		s := configsvc.New(configsvc.NewDemoHelper(), c.(configsvc.AMI), rpt.New(c))
		s.HealthTimeout, s.HealthPoll = 2*time.Second, 20*time.Millisecond
		return s
	}})
}

func TestConfigAuthz(t *testing.T) {
	e := withConfig(t)
	routes := [][2]string{
		{"GET", "/api/config/files"}, {"GET", "/api/config/file?path=rpt.conf"},
		{"POST", "/api/config/preview"}, {"POST", "/api/config/apply"},
		{"GET", "/api/config/history"}, {"GET", "/api/config/history/abcdef1"},
		{"POST", "/api/config/history/abcdef1/restore"}, {"POST", "/api/system/asterisk/restart"},
	}
	for _, role := range []string{"op", "viewer"} {
		for _, r := range routes {
			expectErr(t, e, role, r[0], r[1], map[string]any{}, 403, "forbidden")
		}
	}
}

func TestConfigUnavailable(t *testing.T) {
	e := newEnv(t, opts{})
	expectErr(t, e, "admin", "GET", "/api/config/files", nil, 503, "config_unavailable")
}

type fileJSON struct {
	Path, SHA256 string
	Known        bool
	Group        string
}

func TestConfigPreviewApplyAudit(t *testing.T) {
	e := withConfig(t)
	var files []fileJSON
	if c := e.do("admin", "GET", "/api/config/files", nil, &files); c != 200 || len(files) < 5 {
		t.Fatalf("files: %d %+v", c, files)
	}
	var rc struct{ Content, SHA256 string }
	if c := e.do("admin", "GET", "/api/config/file?path=rpt.conf", nil, &rc); c != 200 || rc.SHA256 == "" {
		t.Fatalf("read: %d", c)
	}
	edited := strings.Replace(rc.Content, "duplex = 1", "duplex = 2", 1)
	body := map[string]any{"files": []map[string]string{{"path": "rpt.conf", "content": edited, "base": rc.SHA256}}}

	var pv struct {
		Files []struct {
			Path    string
			OK      bool
			Changed bool
			Diff    string
		}
		Reload []struct{ Command, Reason string }
	}
	if c := e.do("admin", "POST", "/api/config/preview", body, &pv); c != 200 {
		t.Fatalf("preview: %d", c)
	}
	if len(pv.Files) != 1 || !pv.Files[0].OK || !pv.Files[0].Changed ||
		!strings.Contains(pv.Files[0].Diff, "-duplex = 1") || !strings.Contains(pv.Files[0].Diff, "+duplex = 2") {
		t.Fatalf("preview: %+v", pv.Files)
	}
	if len(pv.Reload) != 1 || pv.Reload[0].Command != "module reload app_rpt.so" {
		t.Fatalf("reload plan: %+v", pv.Reload)
	}
	if n := countCmd(e, "module reload app_rpt.so"); n != 0 {
		t.Fatal("preview must not reload")
	}

	body["message"], body["reload"] = "Change duplex secret-in-message-xyz", true
	var ap struct {
		Commit string
		Files  []string
		Reload []struct {
			Command string
			OK      bool
		}
		Health *struct{ OK bool }
	}
	if c := e.do("admin", "POST", "/api/config/apply", body, &ap); c != 200 {
		t.Fatalf("apply: %d", c)
	}
	if ap.Commit == "" || len(ap.Reload) != 1 || !ap.Reload[0].OK || ap.Health == nil || !ap.Health.OK {
		t.Fatalf("apply: %+v", ap)
	}
	if countCmd(e, "module reload app_rpt.so") != 1 {
		t.Fatal("reload command not sent")
	}

	// stale base -> 409 with the file names
	var cf struct {
		Code  string
		Files []string
	}
	if c := e.do("admin", "POST", "/api/config/apply", body, &cf); c != 409 || cf.Code != "conflict" || len(cf.Files) != 1 || cf.Files[0] != "rpt.conf" {
		t.Fatalf("conflict: %d %+v", c, cf)
	}

	// syntax error -> 422
	bad := map[string]any{"files": []map[string]string{{"path": "rpt.conf", "content": "this is not ini\n", "base": ""}}}
	var se struct {
		Code   string
		Issues []struct{ Path string }
	}
	if c := e.do("admin", "POST", "/api/config/apply", bad, &se); c != 422 || se.Code != "syntax_error" || len(se.Issues) != 1 {
		t.Fatalf("syntax: %d %+v", c, se)
	}

	// masking + reveal audit
	var hist []struct{ Commit, Message string }
	if c := e.do("admin", "GET", "/api/config/history", nil, &hist); c != 200 || len(hist) < 3 {
		t.Fatalf("history: %d %+v", c, hist)
	}
	mp := map[string]any{"files": []map[string]string{{"path": "manager.conf", "content": "[general]\nenabled = yes\n\n[admin]\nsecret = brand-new-secret-value\n", "base": sha(t, e, "manager.conf")}}}
	var ap2 struct{ Commit string }
	if c := e.do("admin", "POST", "/api/config/apply", mp, &ap2); c != 200 {
		t.Fatalf("apply2: %d", c)
	}
	var sh struct{ Files []struct{ Diff string } }
	if c := e.do("admin", "GET", "/api/config/history/"+ap2.Commit, nil, &sh); c != 200 {
		t.Fatal(c)
	}
	d := sh.Files[0].Diff
	if strings.Contains(d, "brand-new-secret-value") || strings.Contains(d, "demo-not-a-real-secret") || !strings.Contains(d, "********") {
		t.Fatalf("not masked:\n%s", d)
	}
	if c := e.do("admin", "GET", "/api/config/history/"+ap2.Commit+"?reveal=1", nil, &sh); c != 200 || !strings.Contains(sh.Files[0].Diff, "brand-new-secret-value") {
		t.Fatalf("reveal: %d", c)
	}
	expectErr(t, e, "admin", "GET", "/api/config/history/zz", nil, 400, "bad_request")

	// audit: actions recorded, no secrets/contents/message
	au, err := e.st.ListAudit(50)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, a := range au {
		seen[a.Action] = true
		for _, bad := range []string{"brand-new-secret-value", "demo-not-a-real-secret", "secret-in-message-xyz", "duplex"} {
			if strings.Contains(a.Detail, bad) {
				t.Fatalf("audit leaks %q: %+v", bad, a)
			}
		}
	}
	for _, a := range []string{"config.read", "config.apply", "config.reveal"} {
		if !seen[a] {
			t.Fatalf("missing audit %s: %v", a, seen)
		}
	}

	// restore the first change
	var rs struct {
		Files   []string
		Skipped []string
		Reload  []struct{ Command string }
	}
	last := hist[len(hist)-1].Commit // initial snapshot
	if c := e.do("admin", "POST", "/api/config/history/"+last+"/restore", map[string]any{"reload": false}, &rs); c != 200 || len(rs.Files) == 0 {
		t.Fatalf("restore: %d %+v", c, rs)
	}
}

func sha(t *testing.T, e *env, path string) string {
	t.Helper()
	var rc struct{ SHA256 string }
	if c := e.do("admin", "GET", "/api/config/file?path="+path, nil, &rc); c != 200 {
		t.Fatal(c)
	}
	return rc.SHA256
}

func TestAsteriskRestartRoute(t *testing.T) {
	e := withConfig(t)
	var out struct{ OK bool }
	if c := e.do("admin", "POST", "/api/system/asterisk/restart", nil, &out); c != 200 || !out.OK {
		t.Fatalf("%d %+v", c, out)
	}
	if countCmd(e, "core restart gracefully") != 1 {
		t.Fatal("no graceful restart")
	}
}
