package api_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
	"github.com/artw1/aslcp/internal/ami/amitest"
	"github.com/artw1/aslcp/internal/api"
	"github.com/artw1/aslcp/internal/auth"
	"github.com/artw1/aslcp/internal/backupsvc"
	"github.com/artw1/aslcp/internal/config"
	"github.com/artw1/aslcp/internal/configsvc"
	"github.com/artw1/aslcp/internal/diag"
	"github.com/artw1/aslcp/internal/favstats"
	"github.com/artw1/aslcp/internal/nodelist"
	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/schedules"
	"github.com/artw1/aslcp/internal/store"
	"github.com/artw1/aslcp/internal/susb"
	"github.com/artw1/aslcp/internal/system"
	"github.com/artw1/aslcp/internal/updates"
)

const localNode = "1997"

// fakeNode answers app_rpt actions from mutable state.
type fakeNode struct {
	*amitest.Server
	mu       sync.Mutex
	rx, tx   bool
	links    []string
	commands []string
}

func newFakeNode(t *testing.T) *fakeNode {
	f := &fakeNode{Server: amitest.New(t), links: []string{"50241"}}
	f.Handle("RptLocalNodes", func(_ *amitest.Session, r *amitest.Request) string {
		return amitest.Raw(`<?xml version="1.0"?>`, `<nodes>`, `  <node>`+localNode+`</node>`, `</nodes>`)
	})
	f.Handle("RptStatus", f.rptStatus)
	f.Handle("Command", func(_ *amitest.Session, r *amitest.Request) string {
		f.mu.Lock()
		defer f.mu.Unlock()
		cmd := r.Get("Command")
		f.commands = append(f.commands, cmd)
		switch cmd {
		case "core show uptime seconds":
			return amitest.CommandOutput(r, "System uptime: 120660", "Last reload: 120660")
		case "core show version":
			return amitest.CommandOutput(r, "Asterisk 22.10.1+asl3-3.10.8-1.deb12 built by x @ y on a aarch64 running Linux on 2026-10-11 15:31:58 UTC")
		}
		if strings.Contains(cmd, "ilink") && strings.Contains(cmd, "unknownnode") {
			return amitest.CommandOutput(r, "Unknown node number")
		}
		return amitest.CommandOutput(r)
	})
	return f
}

func b01(b bool) string {
	if b {
		return "1"
	}
	return "0"
}

func (f *fakeNode) rptStatus(_ *amitest.Session, r *amitest.Request) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch r.Get("Command") {
	case "XStat":
		kv := []string{"Node", localNode}
		var net, al []string
		for _, l := range f.links {
			kv = append(kv, "Conn", fmt.Sprintf("%-10s%-20s%-12d%-11s%-20s%-20s", l, "192.0.2.58", 0, "OUT", "00:00:29", "ESTABLISHED"))
			net = append(net, "T"+l)
			al = append(al, l+"TU")
		}
		ln := "<NONE>"
		if len(net) > 0 {
			ln = strings.Join(net, ", ")
		}
		kv = append(kv, "LinkedNodes", ln,
			"Var", "RPT_RXKEYED="+b01(f.rx), "Var", "RPT_TXKEYED="+b01(f.tx), "Var", "RPT_ETXKEYED=0",
			"Var", fmt.Sprintf("RPT_NUMLINKS=%d", len(net)), "Var", fmt.Sprintf("RPT_NUMALINKS=%d", len(al)))
		kv = append(kv, "parrot_ena", "0", "sys_ena", "0", "tot_ena", "0", "link_ena", "0", "patch_ena", "1",
			"patch_state", "4", "sch_ena", "0", "user_funs", "0", "tail_type", "0", "iconns", "0",
			"tot_state", "2", "ider_state", "2", "tel_mode", "1")
		return amitest.Success(r, kv...)
	case "SawStat":
		kv := []string{"Node", localNode}
		for _, l := range f.links {
			kv = append(kv, "Conn", l+" 0 -1 -1")
		}
		return amitest.Success(r, kv...)
	case "NodeStat":
		return amitest.Success(r, "IsRemoteBase", "NO", "KeyupsToday", "2", "TxTimeToday", "00:00:33:172")
	}
	return amitest.Error(r, "RptStatus unknown command")
}

func (f *fakeNode) setRx(v bool) {
	f.mu.Lock()
	f.rx = v
	f.mu.Unlock()
}

func (f *fakeNode) ilinkCommands() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.commands {
		if strings.HasPrefix(c, "rpt cmd ") {
			out = append(out, c)
		}
	}
	return out
}

type env struct {
	t     *testing.T
	fake  *fakeNode
	st    *store.Store
	as    *auth.Service
	srv   *api.Server
	ts    *httptest.Server
	nl    *nodelist.DB
	mon   *rpt.Monitor
	favDr string
	ck    map[string]string // role -> cookie header
	clk   *fakeClock        // with opts.schedules
	eng   *schedules.Engine // with opts.schedules
}

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

type opts struct {
	noUsers   bool
	dev       bool
	updates   *updates.Manager
	updatesFn func(st *store.Store) *updates.Manager
	power     api.Power
	dvswitch  api.DVSwitch
	config    *configsvc.Service
	configFn  func(rpt.API) *configsvc.Service
	log       *diag.Ring
	cfg       func(secret string) config.Config
	backupFn  func(st *store.Store) *backupsvc.Service
	restart   func()
	tuning    func() *susb.Service
	favstats  *favstats.Service
	// schedules builds a schedule engine on a fake clock (env.clk), in New
	// York time; the clock starts on Tuesday 2026-11-17 18:59:50.
	schedules bool
}

func newEnv(t *testing.T, o opts) *env {
	t.Helper()
	quiet := log.New(io.Discard, "", 0)
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	as, err := auth.NewService(st, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	as.Limiter.Max = 1000 // tests log in often; rate limit has its own test

	fake := newFakeNode(t)
	client := ami.NewClient(ami.Config{Addr: fake.Addr, Username: fake.Username, Secret: fake.Secret, Events: "call",
		ReconnectMin: 20 * time.Millisecond, ReconnectMax: 80 * time.Millisecond})
	t.Cleanup(func() { client.Close() })
	wctx, wcancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer wcancel()
	if err := client.WaitConnected(wctx); err != nil {
		t.Fatal(err)
	}

	nl := nodelist.New(nodelist.Options{Logger: quiet})
	nl.Set([]nodelist.Info{
		{Node: "50241", Callsign: "N0AAC", Description: "Metro Hub", Location: "Springfield, ST"},
		{Node: "2000", Callsign: "N0AAE", Description: "Reflector", Location: "Nowhere, XX"},
		{Node: localNode, Callsign: "N0AAD", Description: "Home node", Location: "Home, ST"},
		{Node: "1999", Callsign: "N0AAA", Description: "Test Hub", Location: "Metropolis, ST"},
	}, "test", time.Now())

	favDir := t.TempDir()
	var origins []string
	if o.dev {
		origins = []string{"http://localhost:5173"}
	}
	deps := api.Deps{
		Auth: as, Store: st, AMI: client, Nodelist: nl, System: system.New("test", client), Updates: o.updates, ConfigEditor: o.config, Version: "test",
		AllowedOrigins: origins, AllScanDirs: []string{favDir}, Logger: quiet,
		Log: o.log, PlatformPaths: &diag.PlatformPaths{DeviceModel: "/nonexistent"},
	}
	if o.updatesFn != nil {
		deps.Updates = o.updatesFn(st)
	}
	deps.Power = o.power
	deps.DVSwitch = o.dvswitch
	if o.backupFn != nil {
		deps.Backup = o.backupFn(st)
		deps.Restart = o.restart
	}
	if o.tuning != nil {
		deps.Tuning = o.tuning()
	}
	deps.FavStats = o.favstats
	var clk *fakeClock
	var eng *schedules.Engine
	if o.schedules {
		ny, err := time.LoadLocation("America/New_York")
		if err != nil {
			t.Fatal(err)
		}
		clk = &fakeClock{t: time.Date(2026, 11, 17, 18, 59, 50, 0, ny)}
		eng = &schedules.Engine{Store: st, Now: clk.now, Loc: ny, Zone: "America/New_York", Tick: time.Hour}
		deps.Schedules = eng
	}
	if o.cfg != nil {
		deps.Config = o.cfg(fake.Secret)
	}
	if o.configFn != nil {
		deps.ConfigEditor = o.configFn(client)
	}
	srv := api.New(deps)
	srv.FastInterval, srv.SlowInterval = 100*time.Millisecond, 400*time.Millisecond
	srv.PingInterval = 100 * time.Millisecond
	srv.SystemInterval = 200 * time.Millisecond
	srv.SetCommandGap(20 * time.Millisecond)
	srv.LinkGoneWait = 300 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	mon := rpt.NewMonitor(client, rpt.MonitorConfig{Nodes: []string{localNode}, Interval: 80 * time.Millisecond})
	go mon.Run(ctx)
	go srv.AttachMonitor(ctx, mon)
	go srv.Run(ctx)

	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		cancel()
		srv.Shutdown()
		ts.Close()
	})
	e := &env{t: t, fake: fake, st: st, as: as, srv: srv, ts: ts, nl: nl, mon: mon, favDr: favDir, ck: map[string]string{}, clk: clk, eng: eng}

	// wait for a real (non-stale) status
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if s, ok := mon.Status(localNode); ok && !s.Stale && !s.Updated.IsZero() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !o.noUsers {
		for _, u := range []struct{ name, role string }{{"admin", "admin"}, {"op", "operator"}, {"viewer", "viewer"}} {
			if _, err := st.CreateUser(u.name, testHash(u.name), u.role); err != nil {
				t.Fatal(err)
			}
		}
		e.session("admin")
		e.session("op")
		e.session("viewer")
	}
	return e
}

// session starts a session directly (argon2 is slow under -race); the HTTP
// login path has its own tests.
func (e *env) session(name string) {
	e.t.Helper()
	u, err := e.st.UserByName(name)
	if err != nil {
		e.t.Fatal(err)
	}
	tok, err := e.as.StartSession(u.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	e.ck[name] = tok
}

var (
	hashMu    sync.Mutex
	hashCache = map[string]string{}
)

// testHash returns a cached argon2id hash of "password-<name>".
func testHash(name string) string {
	hashMu.Lock()
	defer hashMu.Unlock()
	if h, ok := hashCache[name]; ok {
		return h
	}
	h, err := auth.HashPassword("password-" + name)
	if err != nil {
		panic(err)
	}
	hashCache[name] = h
	return h
}

func (e *env) raw(method, path string, body any, cookie string, extra map[string]string) (int, []byte, http.Header) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, e.ts.URL+path, rd)
	if method != "GET" {
		req.Header.Set("X-ASLCP", "1")
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", auth.CookieName+"="+cookie)
	}
	for k, v := range extra {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b, resp.Header
}

// do performs a request as role ("" = anonymous) and decodes JSON into out.
func (e *env) do(role, method, path string, body any, out any) int {
	e.t.Helper()
	code, b, _ := e.raw(method, path, body, e.ck[role], nil)
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			e.t.Fatalf("%s %s: bad JSON %q: %v", method, path, b, err)
		}
	}
	return code
}

func (e *env) errCode(role, method, path string, body any) (int, string) {
	e.t.Helper()
	var er struct{ Error, Code string }
	code := e.do(role, method, path, body, &er)
	return code, er.Code
}

func expectErr(t *testing.T, e *env, role, method, path string, body any, wantStatus int, wantCode string) {
	t.Helper()
	code, c := e.errCode(role, method, path, body)
	if code != wantStatus || c != wantCode {
		t.Fatalf("%s %s as %q: got %d/%q want %d/%q", method, path, role, code, c, wantStatus, wantCode)
	}
}

// ------------------------------------------------------------------ tests

func TestSetupAndSession(t *testing.T) {
	e := newEnv(t, opts{noUsers: true})
	var need struct{ Needed bool }
	if e.do("", "GET", "/api/setup", nil, &need) != 200 || !need.Needed {
		t.Fatal("setup should be needed")
	}
	expectErr(t, e, "", "GET", "/api/nodes", nil, 401, "unauthorized")
	code, err := e.as.EnsureSetupCode()
	if err != nil || code == "" {
		t.Fatal(err)
	}
	expectErr(t, e, "", "POST", "/api/setup", map[string]string{"code": "WRONGWRONG", "username": "boss", "password": "longpassword"}, 403, "bad_setup_code")
	expectErr(t, e, "", "POST", "/api/setup", map[string]string{"code": code, "username": "boss", "password": "short"}, 400, "bad_request")
	st, _, hdr := e.raw("POST", "/api/setup", map[string]string{"code": code, "username": "boss", "password": "longpassword"}, "", nil)
	if st != 204 {
		t.Fatalf("setup: %d", st)
	}
	var cookie *http.Cookie
	for _, c := range (&http.Response{Header: hdr}).Cookies() {
		if c.Name == auth.CookieName {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie: %+v", cookie)
	}
	e.ck["boss"] = cookie.Value
	var u api.UserJSON
	if e.do("boss", "GET", "/api/session", nil, &u) != 200 || u.Username != "boss" || u.Role != "admin" {
		t.Fatalf("session: %+v", u)
	}
	if e.do("", "GET", "/api/setup", nil, &need) != 200 || need.Needed {
		t.Fatal("setup still needed")
	}
	expectErr(t, e, "", "POST", "/api/setup", map[string]string{"code": code, "username": "x2", "password": "longpassword"}, 409, "setup_done")
	// logout invalidates the session server-side
	if c := e.do("boss", "POST", "/api/logout", nil, nil); c != 204 {
		t.Fatal(c)
	}
	expectErr(t, e, "boss", "GET", "/api/session", nil, 401, "unauthorized")
	expectErr(t, e, "", "POST", "/api/login", map[string]string{"username": "boss", "password": "nope"}, 401, "bad_credentials")
	if c := e.do("", "POST", "/api/login", map[string]string{"username": "BOSS", "password": "longpassword"}, &u); c != 200 || u.Username != "boss" {
		t.Fatal(c, u)
	}
}

func TestLoginRateLimit(t *testing.T) {
	e := newEnv(t, opts{})
	e.as.Limiter.Max = 3
	for i := 0; i < 3; i++ {
		expectErr(t, e, "", "POST", "/api/login", map[string]string{"username": "op", "password": "bad"}, 401, "bad_credentials")
	}
	code, _, hdr := e.raw("POST", "/api/login", map[string]string{"username": "op", "password": "password-op"}, "", nil)
	if code != 429 || hdr.Get("Retry-After") == "" {
		t.Fatalf("got %d retry-after=%q", code, hdr.Get("Retry-After"))
	}
	expectErr(t, e, "", "POST", "/api/login", map[string]string{"username": "viewer", "password": "bad"}, 401, "bad_credentials") // other user unaffected
}

func TestCSRFAndOrigin(t *testing.T) {
	e := newEnv(t, opts{})
	body := map[string]string{"username": "op", "password": "password-op"}
	code, b, _ := e.raw("POST", "/api/login", body, "", map[string]string{"X-ASLCP": ""})
	if code != 403 || !strings.Contains(string(b), `"csrf"`) {
		t.Fatalf("missing header: %d %s", code, b)
	}
	code, _, _ = e.raw("POST", "/api/login", body, "", map[string]string{"Origin": "http://evil.example"})
	if code != 403 {
		t.Fatalf("cross-origin: %d", code)
	}
	code, _, _ = e.raw("POST", "/api/login", body, "", map[string]string{"Origin": e.ts.URL})
	if code != 200 {
		t.Fatalf("same-origin: %d", code)
	}
	code, _, _ = e.raw("POST", "/api/login", body, "", map[string]string{"Origin": "http://localhost:5173"})
	if code != 403 {
		t.Fatalf("vite origin without --dev: %d", code)
	}
	// authenticated state-changing request without the header is rejected too
	code, _, _ = e.raw("POST", "/api/nodes/"+localNode+"/disconnect-all", nil, e.ck["op"], map[string]string{"X-ASLCP": ""})
	if code != 403 {
		t.Fatalf("authed no header: %d", code)
	}
	if n := len(e.fake.ilinkCommands()); n != 0 {
		t.Fatalf("command executed despite CSRF rejection (%d)", n)
	}

	d := newEnv(t, opts{dev: true})
	code, _, _ = d.raw("POST", "/api/login", map[string]string{"username": "op", "password": "password-op"}, "", map[string]string{"Origin": "http://localhost:5173"})
	if code != 200 {
		t.Fatalf("vite origin with --dev: %d", code)
	}
}

func TestRoles(t *testing.T) {
	e := newEnv(t, opts{})
	connect := map[string]string{"target": "2000"}
	expectErr(t, e, "viewer", "POST", "/api/nodes/"+localNode+"/connect", connect, 403, "forbidden")
	expectErr(t, e, "viewer", "POST", "/api/nodes/"+localNode+"/disconnect", connect, 403, "forbidden")
	expectErr(t, e, "viewer", "POST", "/api/nodes/"+localNode+"/disconnect-all", nil, 403, "forbidden")
	expectErr(t, e, "viewer", "POST", "/api/favorites", map[string]string{"node": "2000"}, 403, "forbidden")
	expectErr(t, e, "viewer", "GET", "/api/favorites/import/allscan", nil, 403, "forbidden")
	expectErr(t, e, "viewer", "GET", "/api/users", nil, 403, "forbidden")
	expectErr(t, e, "op", "GET", "/api/users", nil, 403, "forbidden")
	expectErr(t, e, "op", "POST", "/api/nodelist/refresh", nil, 403, "forbidden")
	if len(e.fake.ilinkCommands()) != 0 {
		t.Fatal("forbidden request reached AMI")
	}
	if c := e.do("op", "POST", "/api/nodes/"+localNode+"/connect", connect, nil); c != 202 {
		t.Fatalf("operator connect: %d", c)
	}
	if c := e.do("admin", "POST", "/api/nodelist/refresh", nil, nil); c != 202 {
		t.Fatalf("admin refresh: %d", c)
	}
	for _, r := range []string{"viewer", "op", "admin"} {
		for _, p := range []string{"/api/nodes", "/api/favorites", "/api/system", "/api/nodelist", "/api/lookup/2000", "/api/lookup?q=n0"} {
			if c := e.do(r, "GET", p, nil, nil); c != 200 {
				t.Errorf("%s GET %s: %d", r, p, c)
			}
		}
	}
	expectErr(t, e, "admin", "GET", "/api/nope", nil, 404, "not_found")
}

func TestNodesIPsAndInfo(t *testing.T) {
	e := newEnv(t, opts{})
	var admin, viewer []api.NodeStatusJSON
	if e.do("admin", "GET", "/api/nodes", nil, &admin) != 200 || e.do("viewer", "GET", "/api/nodes", nil, &viewer) != 200 {
		t.Fatal("GET /api/nodes")
	}
	if len(admin) != 1 || admin[0].Node != localNode || admin[0].Stale {
		t.Fatalf("%+v", admin)
	}
	n := admin[0]
	if n.Info == nil || n.Info.Callsign != "N0AAD" {
		t.Fatalf("node info: %+v", n.Info)
	}
	if len(n.Links) != 1 {
		t.Fatalf("links: %+v", n.Links)
	}
	l := n.Links[0]
	if l.Node != "50241" || l.IP != "192.0.2.58" || l.Direction != "OUT" || l.State != "ESTABLISHED" ||
		l.Mode != "T" || l.Kind != "allstar" || l.Connected != 29 || l.Info == nil || l.Info.Callsign != "N0AAC" {
		t.Fatalf("link: %+v", l)
	}
	if len(n.Network) != 1 || n.Network[0].Node != "50241" || n.Network[0].Mode != "T" || n.Network[0].Info == nil {
		t.Fatalf("network: %+v", n.Network)
	}
	if n.Flags == nil || n.Flags["txDisabled"] {
		t.Fatalf("flags: %+v", n.Flags)
	}
	if n.Updated == "" {
		t.Fatal("updated missing")
	}
	// viewers never see IPs, anywhere
	if viewer[0].Links[0].IP != "" {
		t.Fatalf("viewer saw IP: %+v", viewer[0].Links[0])
	}
	_, raw, _ := e.raw("GET", "/api/nodes", nil, e.ck["viewer"], nil)
	if bytes.Contains(raw, []byte("192.0.2.58")) {
		t.Fatal("viewer response contains the IP")
	}
	// JSON shape: nulls and empty strings, not omitted
	var generic []map[string]any
	_, raw, _ = e.raw("GET", "/api/nodes", nil, e.ck["viewer"], nil)
	json.Unmarshal(raw, &generic)
	lk := generic[0]["links"].([]any)[0].(map[string]any)
	for _, k := range []string{"node", "kind", "info", "ip", "direction", "connected", "state", "mode", "keyed", "sinceKey", "sinceUnkey"} {
		if _, ok := lk[k]; !ok {
			t.Errorf("link missing key %q", k)
		}
	}
}

func TestConnectDisconnectCommands(t *testing.T) {
	e := newEnv(t, opts{})
	base := "/api/nodes/" + localNode
	steps := []struct {
		path string
		body any
		want string
	}{
		{"/connect", map[string]any{"target": "2000"}, "rpt cmd 1997 ilink 3 2000"},
		{"/connect", map[string]any{"target": "2000", "mode": "transceive"}, "rpt cmd 1997 ilink 3 2000"},
		{"/connect", map[string]any{"target": "2000", "mode": "monitor"}, "rpt cmd 1997 ilink 2 2000"},
		{"/connect", map[string]any{"target": "2000", "mode": "localmonitor"}, "rpt cmd 1997 ilink 8 2000"},
		{"/connect", map[string]any{"target": "2000", "mode": "transceive", "permanent": true}, "rpt cmd 1997 ilink 13 2000"},
		{"/connect", map[string]any{"target": "2000", "mode": "monitor", "permanent": true}, "rpt cmd 1997 ilink 12 2000"},
		{"/connect", map[string]any{"target": "3001234", "mode": "localmonitor", "permanent": true}, "rpt cmd 1997 ilink 18 3001234"},
		{"/disconnect", map[string]any{"target": "2000"}, "rpt cmd 1997 ilink 1 2000"},
		{"/disconnect", map[string]any{"target": "2000", "permanent": true}, "rpt cmd 1997 ilink 11 2000"},
		{"/disconnect", map[string]any{"target": "W1AW-P"}, "rpt cmd 1997 ilink 1 W1AW-P"},
		{"/disconnect-all", nil, "rpt cmd 1997 ilink 6"},
	}
	var want []string
	for _, s := range steps {
		if c := e.do("op", "POST", base+s.path, s.body, nil); c != 202 {
			t.Fatalf("%s %v: %d", s.path, s.body, c)
		}
		want = append(want, s.want)
	}
	got := e.fake.ilinkCommands()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	n := len(e.fake.ilinkCommands())
	for _, bad := range []any{
		map[string]any{"target": ""},
		map[string]any{"target": "0"},
		map[string]any{"target": "-5"},
		map[string]any{"target": "2000; core restart now"},
		map[string]any{"target": "2000\nAction: Logoff"},
		map[string]any{"target": "1997"}, // itself
		map[string]any{"target": "abc"},
	} {
		expectErr(t, e, "op", "POST", base+"/connect", bad, 400, "bad_target")
	}
	expectErr(t, e, "op", "POST", base+"/disconnect", map[string]any{"target": "x y"}, 400, "bad_target")
	expectErr(t, e, "op", "POST", base+"/disconnect", map[string]any{"target": "0"}, 400, "bad_target")
	expectErr(t, e, "op", "POST", base+"/connect", map[string]any{"target": "2000", "mode": "bogus"}, 400, "bad_request")
	if c, _ := e.errCode("op", "POST", base+"/connect", nil); c != 400 {
		t.Fatalf("empty body: %d", c)
	}
	expectErr(t, e, "op", "POST", "/api/nodes/99999/connect", map[string]any{"target": "2000"}, 404, "unknown_node")
	expectErr(t, e, "op", "POST", "/api/nodes/99999/disconnect-all", nil, 404, "unknown_node")
	if len(e.fake.ilinkCommands()) != n {
		t.Fatal("invalid request reached AMI")
	}
}

func TestAMIDown(t *testing.T) {
	e := newEnv(t, opts{})
	e.fake.Stop()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var ns []api.NodeStatusJSON
		e.do("viewer", "GET", "/api/nodes", nil, &ns)
		if len(ns) == 1 && ns[0].Stale {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	expectErr(t, e, "op", "POST", "/api/nodes/"+localNode+"/connect", map[string]any{"target": "2000"}, 503, "ami_down")
	expectErr(t, e, "op", "POST", "/api/nodes/"+localNode+"/disconnect-all", nil, 503, "ami_down")
	var ns []api.NodeStatusJSON
	e.do("viewer", "GET", "/api/nodes", nil, &ns)
	if len(ns) != 1 || !ns[0].Stale {
		t.Fatalf("node should be stale: %+v", ns)
	}
	var sys system.System
	e.do("viewer", "GET", "/api/system", nil, &sys)
	if sys.Asterisk.Connected || sys.Asterisk.Error == nil {
		t.Fatalf("%+v", sys.Asterisk)
	}
}

func TestFavorites(t *testing.T) {
	e := newEnv(t, opts{})
	var f1, f2, f3 api.FavoriteJSON
	if c := e.do("op", "POST", "/api/favorites", map[string]string{"node": "50241"}, &f1); c != 201 {
		t.Fatal(c)
	}
	if f1.Label != "N0AAC Metro Hub" || f1.Info == nil || f1.Info.Callsign != "N0AAC" || f1.Group != "" {
		t.Fatalf("default label: %+v", f1)
	}
	e.do("op", "POST", "/api/favorites", map[string]string{"node": "2000", "label": " My hub ", "group": "Hubs"}, &f2)
	if f2.Label != "My hub" || f2.Group != "Hubs" || f2.Position <= f1.Position {
		t.Fatalf("%+v", f2)
	}
	e.do("op", "POST", "/api/favorites", map[string]string{"node": "424242"}, &f3)
	if f3.Label != "424242" || f3.Info != nil {
		t.Fatalf("unknown node label: %+v", f3)
	}
	expectErr(t, e, "op", "POST", "/api/favorites", map[string]string{"node": "abc"}, 400, "bad_target")
	expectErr(t, e, "op", "POST", "/api/favorites", map[string]string{"node": "2000", "label": strings.Repeat("x", 500)}, 400, "bad_request")

	var list []api.FavoriteJSON
	e.do("viewer", "GET", "/api/favorites", nil, &list)
	if len(list) != 3 || list[0].ID != f1.ID || list[1].ID != f3.ID && list[1].ID != f2.ID {
		t.Fatalf("%+v", list)
	}
	// ordered by group then position: "" group first
	if list[0].Group != "" || list[2].Group != "Hubs" {
		t.Fatalf("order: %+v", list)
	}

	var up api.FavoriteJSON
	if c := e.do("op", "PUT", fmt.Sprintf("/api/favorites/%d", f2.ID), map[string]string{"label": "Renamed", "group": ""}, &up); c != 200 || up.Label != "Renamed" || up.Group != "" || up.Node != "2000" {
		t.Fatal(c, up)
	}
	if c := e.do("op", "PUT", fmt.Sprintf("/api/favorites/%d", f2.ID), map[string]string{"node": "1999"}, &up); c != 200 || up.Node != "1999" || up.Info == nil || up.Info.Callsign != "N0AAA" {
		t.Fatal(c, up)
	}
	expectErr(t, e, "op", "PUT", "/api/favorites/9999", map[string]string{"label": "x"}, 404, "not_found")
	expectErr(t, e, "op", "PUT", fmt.Sprintf("/api/favorites/%d", f2.ID), map[string]string{"node": "bad"}, 400, "bad_target")

	// reorder: all in one group now, positions follow array order
	if c := e.do("op", "POST", "/api/favorites/reorder", map[string][]int64{"ids": {f3.ID, f2.ID, f1.ID}}, nil); c != 204 {
		t.Fatal(c)
	}
	e.do("op", "GET", "/api/favorites", nil, &list)
	if len(list) != 3 || list[0].ID != f3.ID || list[1].ID != f2.ID || list[2].ID != f1.ID {
		t.Fatalf("after reorder: %+v", list)
	}
	for i, f := range list {
		if f.Position != i {
			t.Fatalf("positions: %+v", list)
		}
	}
	expectErr(t, e, "op", "POST", "/api/favorites/reorder", map[string][]int64{"ids": {12345}}, 404, "not_found")

	if c := e.do("op", "DELETE", fmt.Sprintf("/api/favorites/%d", f1.ID), nil, nil); c != 204 {
		t.Fatal(c)
	}
	expectErr(t, e, "op", "DELETE", fmt.Sprintf("/api/favorites/%d", f1.ID), nil, 404, "not_found")
	e.do("op", "GET", "/api/favorites", nil, &list)
	if len(list) != 2 {
		t.Fatalf("%+v", list)
	}
}

func TestImportAllScan(t *testing.T) {
	e := newEnv(t, opts{})
	var pre struct {
		Found bool
		Path  string
		Items []map[string]string
	}
	if c := e.do("op", "GET", "/api/favorites/import/allscan", nil, &pre); c != 200 || pre.Found || pre.Items == nil || len(pre.Items) != 0 {
		t.Fatalf("no install: %d %+v", c, pre)
	}
	sample := "[general]\nlabel[] = \"Hub A 2000\"\ncmd[] = \"rpt cmd %node% ilink 3 2000\"\n" +
		"label[] = \"Hub B 1999\"\ncmd[] = \"rpt cmd %node% ilink 3 1999\"\n" +
		"label[] = \"Dup\"\ncmd[] = \"rpt cmd %node% ilink 3 2000\"\n" +
		"label[] = \"Macro\"\ncmd[] = \"rpt fun %node% *81\"\n"
	if err := os.WriteFile(filepath.Join(e.favDr, "favorites.ini"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if c := e.do("op", "GET", "/api/favorites/import/allscan", nil, &pre); c != 200 || !pre.Found || len(pre.Items) != 2 ||
		pre.Items[0]["node"] != "2000" || pre.Items[0]["label"] != "Hub A 2000" || !strings.HasSuffix(pre.Path, "favorites.ini") {
		t.Fatalf("preview: %d %+v", c, pre)
	}
	// existing favorite is skipped
	e.do("op", "POST", "/api/favorites", map[string]string{"node": "2000", "label": "Mine"}, nil)
	var res map[string]int
	if c := e.do("op", "POST", "/api/favorites/import/allscan", nil, &res); c != 200 || res["imported"] != 1 || res["skipped"] != 1 {
		t.Fatalf("import: %d %+v", c, res)
	}
	var list []api.FavoriteJSON
	e.do("op", "GET", "/api/favorites", nil, &list)
	if len(list) != 2 || list[0].Label != "Mine" || list[1].Node != "1999" || list[1].Label != "Hub B 1999" {
		t.Fatalf("%+v", list)
	}
	// importing again changes nothing
	e.do("op", "POST", "/api/favorites/import/allscan", nil, &res)
	if res["imported"] != 0 || res["skipped"] != 2 {
		t.Fatalf("second import: %+v", res)
	}
}

func TestLookup(t *testing.T) {
	e := newEnv(t, opts{})
	var in nodelist.Info
	if c := e.do("viewer", "GET", "/api/lookup/50241", nil, &in); c != 200 || in.Callsign != "N0AAC" || in.Location != "Springfield, ST" {
		t.Fatal(c, in)
	}
	expectErr(t, e, "viewer", "GET", "/api/lookup/777", nil, 404, "not_found")
	var res []nodelist.Info
	if c := e.do("viewer", "GET", "/api/lookup?q=n0aa&limit=2", nil, &res); c != 200 || len(res) != 2 {
		t.Fatal(c, res)
	}
	e.do("viewer", "GET", "/api/lookup?q=metropolis", nil, &res)
	if len(res) != 1 || res[0].Node != "1999" {
		t.Fatalf("%+v", res)
	}
	e.do("viewer", "GET", "/api/lookup?q=20", nil, &res)
	if len(res) != 1 || res[0].Node != "2000" {
		t.Fatalf("prefix: %+v", res)
	}
	e.do("viewer", "GET", "/api/lookup", nil, &res)
	if res == nil || len(res) != 0 {
		t.Fatalf("empty q: %+v", res)
	}
	expectErr(t, e, "viewer", "GET", "/api/lookup?q=x&limit=-1", nil, 400, "bad_request")
	var st nodelist.Status
	if c := e.do("viewer", "GET", "/api/nodelist", nil, &st); c != 200 || st.Count != 4 || st.Source != "test" || st.Updated == nil || st.Error != nil {
		t.Fatal(c, st)
	}
}

func TestSystem(t *testing.T) {
	e := newEnv(t, opts{})
	var sys system.System
	if c := e.do("viewer", "GET", "/api/system", nil, &sys); c != 200 {
		t.Fatal(c)
	}
	if !sys.Asterisk.Connected || sys.Asterisk.Version != "22.10.1+asl3-3.10.8-1.deb12" || sys.Asterisk.Uptime == nil || *sys.Asterisk.Uptime != 120660 || sys.Version != "test" {
		t.Fatalf("%+v", sys)
	}
	_, raw, _ := e.raw("GET", "/api/system", nil, e.ck["viewer"], nil)
	for _, k := range []string{`"hostname"`, `"cpuTemp"`, `"load"`, `"memTotal"`, `"memAvailable"`, `"uptime"`, `"asterisk"`, `"version"`, `"connected"`, `"error"`} {
		if !bytes.Contains(raw, []byte(k)) {
			t.Errorf("missing %s in %s", k, raw)
		}
	}
}

func TestUsersAdmin(t *testing.T) {
	e := newEnv(t, opts{})
	var u api.UserJSON
	if c := e.do("admin", "POST", "/api/users", map[string]string{"username": "newbie", "password": "longpassword", "role": "viewer"}, &u); c != 201 || u.Role != "viewer" || u.ID == 0 {
		t.Fatal(c, u)
	}
	expectErr(t, e, "admin", "POST", "/api/users", map[string]string{"username": "NEWBIE", "password": "longpassword", "role": "viewer"}, 409, "exists")
	expectErr(t, e, "admin", "POST", "/api/users", map[string]string{"username": "x", "password": "short", "role": "viewer"}, 400, "bad_request")
	expectErr(t, e, "admin", "POST", "/api/users", map[string]string{"username": "x", "password": "longpassword", "role": "root"}, 400, "bad_request")
	var list []api.UserJSON
	e.do("admin", "GET", "/api/users", nil, &list)
	if len(list) != 4 {
		t.Fatalf("%+v", list)
	}
	_, raw, _ := e.raw("GET", "/api/users", nil, e.ck["admin"], nil)
	if bytes.Contains(raw, []byte("argon2")) || bytes.Contains(raw, []byte("pw_hash")) {
		t.Fatal("password hash leaked")
	}
	path := fmt.Sprintf("/api/users/%d", u.ID)
	// promote + reset password; new password works, old sessions die
	if c := e.do("admin", "PUT", path, map[string]string{"role": "operator", "password": "another-long-pw"}, &u); c != 200 || u.Role != "operator" {
		t.Fatal(c, u)
	}
	if c := e.do("", "POST", "/api/login", map[string]string{"username": "newbie", "password": "another-long-pw"}, nil); c != 200 {
		t.Fatalf("login with new password: %d", c)
	}
	expectErr(t, e, "admin", "PUT", "/api/users/9999", map[string]string{"role": "viewer"}, 404, "not_found")
	expectErr(t, e, "admin", "PUT", path, map[string]string{"role": "king"}, 400, "bad_request")

	var me api.UserJSON
	e.do("admin", "GET", "/api/session", nil, &me)
	selfPath := fmt.Sprintf("/api/users/%d", me.ID)
	expectErr(t, e, "admin", "DELETE", selfPath, nil, 409, "self_delete")
	expectErr(t, e, "admin", "PUT", selfPath, map[string]string{"role": "viewer"}, 409, "last_admin")
	if c := e.do("admin", "DELETE", path, nil, nil); c != 204 {
		t.Fatal(c)
	}
	expectErr(t, e, "admin", "DELETE", path, nil, 404, "not_found")
	// second admin allows demoting the first; last admin cannot be deleted by the other
	var a2 api.UserJSON
	e.do("admin", "POST", "/api/users", map[string]string{"username": "boss2", "password": "longpassword", "role": "admin"}, &a2)
	if c := e.do("admin", "PUT", selfPath, map[string]string{"role": "operator"}, nil); c != 200 {
		t.Fatalf("demote with 2 admins: %d", c)
	}
}

func TestPasswordChange(t *testing.T) {
	e := newEnv(t, opts{})
	expectErr(t, e, "op", "POST", "/api/password", map[string]string{"current": "wrong", "new": "brand-new-password"}, 403, "bad_credentials")
	expectErr(t, e, "op", "POST", "/api/password", map[string]string{"current": "password-op", "new": "short"}, 400, "bad_request")
	// a second device
	code, _, hdr := e.raw("POST", "/api/login", map[string]string{"username": "op", "password": "password-op"}, "", nil)
	if code != 200 {
		t.Fatal(code)
	}
	var other string
	for _, c := range hdr.Values("Set-Cookie") {
		other = strings.SplitN(strings.SplitN(c, ";", 2)[0], "=", 2)[1]
	}
	if c := e.do("op", "POST", "/api/password", map[string]string{"current": "password-op", "new": "brand-new-password"}, nil); c != 204 {
		t.Fatal(c)
	}
	if c := e.do("op", "GET", "/api/session", nil, nil); c != 200 {
		t.Fatalf("current session should survive: %d", c)
	}
	if c, _, _ := e.raw("GET", "/api/session", nil, other, nil); c != 401 {
		t.Fatalf("other session should be revoked: %d", c)
	}
	if c := e.do("", "POST", "/api/login", map[string]string{"username": "op", "password": "brand-new-password"}, nil); c != 200 {
		t.Fatalf("login with new password: %d", c)
	}
}

func TestSecurityHeadersAndNoStore(t *testing.T) {
	e := newEnv(t, opts{})
	_, _, h := e.raw("GET", "/api/setup", nil, "", nil)
	if !strings.Contains(h.Get("Content-Security-Policy"), "default-src 'self'") ||
		!strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") ||
		h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") == "" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers: %v", h)
	}
}

// ------------------------------------------------------------------- SSE

type sseEvent struct {
	name, data string
}

type sseStream struct {
	events  chan sseEvent
	pings   chan struct{}
	cancel  context.CancelFunc
	closed  chan struct{}
	resp    *http.Response
	t       *testing.T
	pending []sseEvent
}

func (e *env) stream(role string) *sseStream {
	e.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/events", nil)
	req.Header.Set("Cookie", auth.CookieName+"="+e.ck[role])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		e.t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		cancel()
		e.t.Fatalf("events: %d %s", resp.StatusCode, b)
	}
	s := &sseStream{events: make(chan sseEvent, 1000), pings: make(chan struct{}, 1000), cancel: cancel, closed: make(chan struct{}), resp: resp, t: e.t}
	go func() {
		defer close(s.closed)
		defer resp.Body.Close()
		br := bufio.NewReader(resp.Body)
		var cur sseEvent
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			switch {
			case strings.HasPrefix(line, ": ping"):
				select {
				case s.pings <- struct{}{}:
				default:
				}
			case strings.HasPrefix(line, "event: "):
				cur.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				cur.data = strings.TrimPrefix(line, "data: ")
			case line == "" && cur.name != "":
				s.events <- cur
				cur = sseEvent{}
			}
		}
	}()
	e.t.Cleanup(cancel)
	return s
}

// next waits for an event satisfying pred (earlier non-matching events are skipped).
func (s *sseStream) wait(pred func(sseEvent) bool) sseEvent {
	s.t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev := <-s.events:
			if pred(ev) {
				return ev
			}
		case <-timeout:
			s.t.Fatal("timed out waiting for SSE event")
		}
	}
}

func (s *sseStream) first() sseEvent {
	s.t.Helper()
	select {
	case ev := <-s.events:
		return ev
	case <-time.After(5 * time.Second):
		s.t.Fatal("timed out waiting for first SSE event")
	}
	return sseEvent{}
}

func TestInstanceHeaderMatchesHello(t *testing.T) {
	e := newEnv(t, opts{})
	_, _, h := e.raw("GET", "/api/session", nil, e.ck["admin"], nil)
	inst := h.Get("X-ASLCP-Instance")
	if inst == "" {
		t.Fatal("missing X-ASLCP-Instance header")
	}
	var hello struct {
		Instance string `json:"instance"`
	}
	if err := json.Unmarshal([]byte(e.stream("admin").first().data), &hello); err != nil || hello.Instance != inst {
		t.Fatalf("hello instance %q header %q (%v)", hello.Instance, inst, err)
	}
}

func TestSSEHelloNodesAndKeying(t *testing.T) {
	e := newEnv(t, opts{})
	s := e.stream("admin")
	if ct := s.resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type %q", ct)
	}
	ev := s.first()
	if ev.name != "hello" {
		t.Fatalf("first event %q", ev.name)
	}
	var hello struct {
		Instance string       `json:"instance"`
		User     api.UserJSON `json:"user"`
		Version  string       `json:"version"`
	}
	if err := json.Unmarshal([]byte(ev.data), &hello); err != nil || hello.User.Username != "admin" || hello.Version != "test" || hello.Instance == "" {
		t.Fatalf("hello: %s %v", ev.data, err)
	}
	ev = s.first()
	if ev.name != "nodes" {
		t.Fatalf("second event %q", ev.name)
	}
	var nodes []api.NodeStatusJSON
	if err := json.Unmarshal([]byte(ev.data), &nodes); err != nil || len(nodes) != 1 || nodes[0].Node != localNode || nodes[0].RxKeyed {
		t.Fatalf("nodes: %s %v", ev.data, err)
	}
	if nodes[0].Links[0].IP != "192.0.2.58" {
		t.Fatal("admin stream should include IPs")
	}

	// 1 s polling while a client is connected
	deadline := time.Now().Add(2 * time.Second)
	for e.mon.Interval() != e.srv.FastInterval && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e.mon.Interval() != e.srv.FastInterval {
		t.Fatalf("interval with client: %v", e.mon.Interval())
	}

	// a keying event produces a node event
	e.fake.setRx(true)
	e.fake.PushEvent("RPT_RXKEYED", "Node", localNode, "Channel", "SimpleUSB/"+localNode, "EventValue", "1")
	ev = s.wait(func(ev sseEvent) bool {
		if ev.name != "node" {
			return false
		}
		var n api.NodeStatusJSON
		return json.Unmarshal([]byte(ev.data), &n) == nil && n.RxKeyed
	})
	var n api.NodeStatusJSON
	json.Unmarshal([]byte(ev.data), &n)
	if n.Node != localNode {
		t.Fatalf("%+v", n)
	}
	// and un-keying
	e.fake.setRx(false)
	e.fake.PushEvent("RPT_RXKEYED", "Node", localNode, "Channel", "SimpleUSB/"+localNode, "EventValue", "0")
	s.wait(func(ev sseEvent) bool {
		var n api.NodeStatusJSON
		return ev.name == "node" && json.Unmarshal([]byte(ev.data), &n) == nil && !n.RxKeyed
	})

	// pings (test interval 100 ms)
	select {
	case <-s.pings:
	case <-time.After(3 * time.Second):
		t.Fatal("no ping comment")
	}
	// system pushes (test interval 200 ms)
	ev = s.wait(func(ev sseEvent) bool { return ev.name == "system" })
	var sys system.System
	if err := json.Unmarshal([]byte(ev.data), &sys); err != nil || !sys.Asterisk.Connected {
		t.Fatalf("system event: %s %v", ev.data, err)
	}

	// disconnect: slow polling again
	s.cancel()
	<-s.closed
	deadline = time.Now().Add(3 * time.Second)
	for e.mon.Interval() != e.srv.SlowInterval && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if e.mon.Interval() != e.srv.SlowInterval || e.srv.Clients() != 0 {
		t.Fatalf("interval after disconnect: %v clients=%d", e.mon.Interval(), e.srv.Clients())
	}
}

func TestSSEViewerNoIPsAndFavoritesEvent(t *testing.T) {
	e := newEnv(t, opts{})
	v := e.stream("viewer")
	if v.first().name != "hello" {
		t.Fatal("hello")
	}
	ev := v.first()
	if ev.name != "nodes" || strings.Contains(ev.data, "192.0.2.58") {
		t.Fatalf("viewer nodes event leaks IP: %s", ev.data)
	}
	// favorites event reaches every client
	e.do("op", "POST", "/api/favorites", map[string]string{"node": "2000", "label": "Via SSE"}, nil)
	ev = v.wait(func(ev sseEvent) bool { return ev.name == "favorites" })
	var favs []api.FavoriteJSON
	if err := json.Unmarshal([]byte(ev.data), &favs); err != nil || len(favs) != 1 || favs[0].Label != "Via SSE" {
		t.Fatalf("%s %v", ev.data, err)
	}
	// a node event for a viewer also has no IP: force one with a keying change
	e.fake.setRx(true)
	e.fake.PushEvent("RPT_RXKEYED", "Node", localNode, "Channel", "x", "EventValue", "1")
	ev = v.wait(func(ev sseEvent) bool { return ev.name == "node" })
	if strings.Contains(ev.data, "192.0.2.58") {
		t.Fatalf("viewer node event leaks IP: %s", ev.data)
	}
}

func TestSSERequiresSessionAndEndsOnLogout(t *testing.T) {
	e := newEnv(t, opts{})
	if c, _, _ := e.raw("GET", "/api/events", nil, "", nil); c != 401 {
		t.Fatalf("anonymous events: %d", c)
	}
	s := e.stream("op")
	s.first()
	e.do("op", "POST", "/api/logout", nil, nil)
	select {
	case <-s.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("stream not closed after logout")
	}
}

func TestSSENodesAfterReconnect(t *testing.T) {
	e := newEnv(t, opts{})
	s := e.stream("admin")
	s.first()
	s.first()
	e.fake.DropConnections()
	// stale node event, then a full "nodes" once the client reconnected and recovered
	s.wait(func(ev sseEvent) bool {
		var n api.NodeStatusJSON
		return ev.name == "node" && json.Unmarshal([]byte(ev.data), &n) == nil && n.Stale
	})
	ev := s.wait(func(ev sseEvent) bool { return ev.name == "nodes" })
	var nodes []api.NodeStatusJSON
	if err := json.Unmarshal([]byte(ev.data), &nodes); err != nil || len(nodes) != 1 || nodes[0].Stale {
		t.Fatalf("%s %v", ev.data, err)
	}
}
