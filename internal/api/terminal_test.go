package api_test

import (
	"context"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/artw1/aslcp/internal/auth"
	"github.com/artw1/aslcp/internal/helper"
)

// termSys is a fake helper with terminal support: the "shell" echoes keyboard
// input back prefixed with "echo:" and records resizes.
type termSys struct {
	*fakeSys
	tmu     sync.Mutex
	opened  int
	sizes   []string
	rows    uint16
	cols    uint16
	hangups chan struct{}
}

func newTermSys() *termSys {
	return &termSys{fakeSys: newFakeSys(), hangups: make(chan struct{}, 16)}
}

func (f *termSys) OpenTerminal(_ context.Context, rows, cols uint16) (io.ReadWriteCloser, error) {
	f.tmu.Lock()
	f.opened++
	f.rows, f.cols = rows, cols
	f.tmu.Unlock()
	client, server := net.Pipe()
	go func() {
		defer func() {
			server.Close()
			f.hangups <- struct{}{}
		}()
		if helper.WriteFrame(server, helper.FrameUser, []byte("alice")) != nil {
			return
		}
		for {
			typ, p, err := helper.ReadFrame(server)
			if err != nil {
				return
			}
			switch typ {
			case helper.FrameData:
				if helper.WriteData(server, append([]byte("echo:"), p...)) != nil {
					return
				}
			case helper.FrameResize:
				r, c, _ := helper.ParseResize(p)
				f.tmu.Lock()
				f.sizes = append(f.sizes, strconv.Itoa(int(r))+"x"+strconv.Itoa(int(c)))
				f.tmu.Unlock()
			}
		}
	}()
	return client, nil
}

func (f *termSys) sizeList() string {
	f.tmu.Lock()
	defer f.tmu.Unlock()
	return strings.Join(f.sizes, ",")
}

func (f *termSys) openCount() int {
	f.tmu.Lock()
	defer f.tmu.Unlock()
	return f.opened
}

func (e *env) termOn() {
	e.t.Helper()
	var st map[string]any
	if c := e.do("admin", "PUT", "/api/system/terminal", map[string]any{"enabled": true}, &st); c != 200 || st["enabled"] != true {
		e.t.Fatalf("enable: %d %v", c, st)
	}
}

func (e *env) ticket(role string) string {
	e.t.Helper()
	var out struct {
		Ticket    string `json:"ticket"`
		ExpiresIn int    `json:"expiresIn"`
	}
	if c := e.do(role, "POST", "/api/system/terminal/ticket", nil, &out); c != 200 || out.Ticket == "" || out.ExpiresIn != 30 {
		e.t.Fatalf("ticket: %d %+v", c, out)
	}
	return out.Ticket
}

// dialWS opens the terminal WebSocket as role. origin "" is the panel's own,
// "-" sends none.
func (e *env) dialWS(role, query, origin string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	if c := e.ck[role]; c != "" {
		h.Set("Cookie", auth.CookieName+"="+c)
	}
	if origin == "" {
		origin = e.ts.URL
	}
	if origin != "-" {
		h.Set("Origin", origin)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(e.ts.URL, "http")+"/api/system/terminal/ws"+query, &websocket.DialOptions{HTTPHeader: h})
}

func refused(resp *http.Response, err error, code int) bool {
	return err != nil && resp != nil && resp.StatusCode == code
}

func readText(t *testing.T, c *websocket.Conn, want string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got string
	for !strings.Contains(got, want) {
		_, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (got %q, want %q)", err, got, want)
		}
		got += string(b)
	}
}

func termWait(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func (e *env) auditActions() string {
	l, _ := e.st.ListAudit(100)
	var s []string
	for _, a := range l {
		s = append(s, a.Action+" "+a.Detail)
	}
	return strings.Join(s, "\n")
}

func (e *env) termSessions() int {
	var st struct{ Sessions int }
	e.do("admin", "GET", "/api/system/terminal", nil, &st)
	return st.Sessions
}

func TestTerminalDefaultOffAndRoles(t *testing.T) {
	e := newEnv(t, opts{sysHost: newTermSys()})
	var st struct {
		Enabled, Available bool
		MaxSessions        int
	}
	if c := e.do("admin", "GET", "/api/system/terminal", nil, &st); c != 200 || st.Enabled || !st.Available || st.MaxSessions != 2 {
		t.Fatalf("%d %+v", c, st)
	}
	if c, code := e.errCode("admin", "POST", "/api/system/terminal/ticket", nil); c != 403 || code != "terminal_disabled" {
		t.Fatalf("ticket while off: %d %s", c, code)
	}
	for _, role := range []string{"op", "viewer", ""} {
		want := 403
		if role == "" {
			want = 401
		}
		for _, r := range []struct{ m, p string }{{"GET", "/api/system/terminal"}, {"PUT", "/api/system/terminal"}, {"POST", "/api/system/terminal/ticket"}, {"GET", "/api/system/terminal/ws"}} {
			var body any
			if r.m == "PUT" {
				body = map[string]any{"enabled": true}
			}
			if c := e.do(role, r.m, r.p, body, nil); c != want {
				t.Errorf("%q %s %s = %d, want %d", role, r.m, r.p, c, want)
			}
		}
	}
	if c := e.do("admin", "GET", "/api/system/terminal", nil, &st); c != 200 || st.Enabled {
		t.Fatalf("enabled by a non-admin: %+v", st)
	}
}

func TestTerminalNoHelperSupport(t *testing.T) {
	e := newEnv(t, opts{sysHost: newFakeSys()}) // no OpenTerminal
	e.termOn()
	if c, code := e.errCode("admin", "POST", "/api/system/terminal/ticket", nil); c != 503 || code != "system_unavailable" {
		t.Fatalf("%d %s", c, code)
	}
	e2 := newEnv(t, opts{})
	if c := e2.do("admin", "POST", "/api/system/terminal/ticket", nil, nil); c != 503 {
		t.Fatalf("no helper: %d", c)
	}
}

func TestTerminalTicketNeedsCSRFHeader(t *testing.T) {
	e := newEnv(t, opts{sysHost: newTermSys()})
	e.termOn()
	c, _, _ := e.raw("POST", "/api/system/terminal/ticket", nil, e.ck["admin"], map[string]string{"X-ASLCP": ""})
	if c != 403 {
		t.Fatalf("POST without the CSRF header = %d", c)
	}
}

func TestTerminalRelayEndToEnd(t *testing.T) {
	fs := newTermSys()
	e := newEnv(t, opts{sysHost: fs})
	e.termOn()
	c, _, err := e.dialWS("admin", "?ticket="+e.ticket("admin")+"&rows=30&cols=100", "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	ctx := context.Background()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("ls\r")); err != nil {
		t.Fatal(err)
	}
	readText(t, c, "echo:ls\r")
	if err := c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","rows":50,"cols":132}`)); err != nil {
		t.Fatal(err)
	}
	// invalid ones are ignored
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","rows":0,"cols":132}`))
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"resize","rows":9999,"cols":1}`))
	_ = c.Write(ctx, websocket.MessageText, []byte(`not json`))
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"type":"other","rows":5,"cols":5}`))
	termWait(t, "resize", func() bool { return fs.sizeList() == "50x132" })
	fs.tmu.Lock()
	if fs.rows != 30 || fs.cols != 100 {
		t.Errorf("initial size %dx%d", fs.rows, fs.cols)
	}
	fs.tmu.Unlock()
	time.Sleep(50 * time.Millisecond)
	if got := fs.sizeList(); got != "50x132" {
		t.Errorf("invalid resizes got through: %s", got)
	}

	_ = c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-fs.hangups:
	case <-time.After(5 * time.Second):
		t.Fatal("helper stream not closed after the WebSocket closed")
	}
	termWait(t, "end audit", func() bool { return strings.Contains(e.auditActions(), "system.terminal.end") })
	a := e.auditActions()
	for _, w := range []string{"system.terminal.start", `"reason":"closed"`, `"linuxUser":"alice"`, `"seconds":`} {
		if !strings.Contains(a, w) {
			t.Errorf("audit lacks %q:\n%s", w, a)
		}
	}
	if strings.Contains(a, "ls") && strings.Contains(a, "echo:") {
		t.Errorf("audit holds terminal data:\n%s", a)
	}
	if strings.Contains(a, "echo:") {
		t.Errorf("audit holds terminal output:\n%s", a)
	}
}

func TestTerminalTicketRules(t *testing.T) {
	fs := newTermSys()
	e := newEnv(t, opts{sysHost: fs})
	e.termOn()

	// single use
	tk := e.ticket("admin")
	c, _, err := e.dialWS("admin", "?ticket="+tk, "")
	if err != nil {
		t.Fatal(err)
	}
	c.CloseNow()
	if _, resp, err := e.dialWS("admin", "?ticket="+tk, ""); !refused(resp, err, 403) {
		t.Fatalf("ticket reused: %v %v", err, resp)
	}
	// missing and bogus
	for _, q := range []string{"", "?ticket=", "?ticket=nope"} {
		if _, resp, err := e.dialWS("admin", q, ""); !refused(resp, err, 403) {
			t.Errorf("%q accepted: %v", q, err)
		}
	}
	// tied to the login session: a second login does not share tickets
	old := e.ticket("admin")
	e.session("admin")
	if _, resp, err := e.dialWS("admin", "?ticket="+old, ""); !refused(resp, err, 403) {
		t.Errorf("ticket used from another session: %v", err)
	}
	// an operator cannot use an admin's ticket (and is refused anyway)
	tk = e.ticket("admin")
	if _, resp, err := e.dialWS("op", "?ticket="+tk, ""); !refused(resp, err, 403) {
		t.Errorf("operator reached the terminal: %v", err)
	}
	if n := fs.openCount(); n != 1 {
		t.Errorf("%d terminals opened, want 1", n)
	}
}

func TestTerminalOriginChecked(t *testing.T) {
	fs := newTermSys()
	e := newEnv(t, opts{sysHost: fs, dev: true})
	e.termOn()
	for _, origin := range []string{"https://attacker.example", "http://127.0.0.1:1", "-"} {
		if _, resp, err := e.dialWS("admin", "?ticket="+e.ticket("admin"), origin); !refused(resp, err, 403) {
			t.Errorf("origin %q accepted: %v", origin, err)
		}
	}
	if n := fs.openCount(); n != 0 {
		t.Fatalf("%d terminals opened by a bad origin", n)
	}
	// an allowed extra origin (dev server) works
	c, _, err := e.dialWS("admin", "?ticket="+e.ticket("admin"), "http://localhost:5173")
	if err != nil {
		t.Fatalf("allowed origin refused: %v", err)
	}
	c.CloseNow()
}

func TestTerminalSessionLimit(t *testing.T) {
	fs := newTermSys()
	e := newEnv(t, opts{sysHost: fs})
	e.termOn()
	var conns []*websocket.Conn
	for i := 0; i < 2; i++ {
		c, _, err := e.dialWS("admin", "?ticket="+e.ticket("admin"), "")
		if err != nil {
			t.Fatal(err)
		}
		conns = append(conns, c)
	}
	termWait(t, "two sessions", func() bool { return e.termSessions() == 2 })
	if c, code := e.errCode("admin", "POST", "/api/system/terminal/ticket", nil); c != 429 || code != "too_many_sessions" {
		t.Fatalf("third ticket: %d %s", c, code)
	}
	conns[0].CloseNow()
	termWait(t, "slot freed", func() bool {
		c, _ := e.errCode("admin", "POST", "/api/system/terminal/ticket", nil)
		return c == 200
	})
	conns[1].CloseNow()
}

func TestTerminalDisableEndsSessions(t *testing.T) {
	fs := newTermSys()
	e := newEnv(t, opts{sysHost: fs})
	e.termOn()
	c, _, err := e.dialWS("admin", "?ticket="+e.ticket("admin"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	termWait(t, "session up", func() bool { return e.termSessions() == 1 })
	if code := e.do("admin", "PUT", "/api/system/terminal", map[string]any{"enabled": false}, nil); code != 200 {
		t.Fatal(code)
	}
	select {
	case <-fs.hangups:
	case <-time.After(5 * time.Second):
		t.Fatal("session survived the switch being turned off")
	}
	drain(c)
	termWait(t, "audit", func() bool { return strings.Contains(e.auditActions(), `"reason":"disabled"`) })
	if code, _ := e.errCode("admin", "POST", "/api/system/terminal/ticket", nil); code != 403 {
		t.Fatalf("ticket after disable: %d", code)
	}
	if !strings.Contains(e.auditActions(), "system.terminal {") {
		t.Errorf("setting change not audited:\n%s", e.auditActions())
	}
}

func TestTerminalIdleTimeout(t *testing.T) {
	fs := newTermSys()
	e := newEnv(t, opts{sysHost: fs})
	var mu sync.Mutex
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	e.srv.SetTerminalLimits(0, 0, 0, 20*time.Millisecond, clock)
	e.termOn()
	c, _, err := e.dialWS("admin", "?ticket="+e.ticket("admin"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	termWait(t, "session up", func() bool { return e.termSessions() == 1 })
	adv := func(d time.Duration) { mu.Lock(); now = now.Add(d); mu.Unlock() }

	// typing resets the timer; resizing does not
	adv(14 * time.Minute)
	_ = c.Write(context.Background(), websocket.MessageBinary, []byte("x"))
	readText(t, c, "echo:x")
	adv(14 * time.Minute)
	_ = c.Write(context.Background(), websocket.MessageText, []byte(`{"type":"resize","rows":10,"cols":10}`))
	time.Sleep(150 * time.Millisecond)
	select {
	case <-fs.hangups:
		t.Fatal("session ended before 15 idle minutes")
	default:
	}
	adv(2 * time.Minute)
	select {
	case <-fs.hangups:
	case <-time.After(5 * time.Second):
		t.Fatal("idle session not ended")
	}
	drain(c)
	termWait(t, "audit", func() bool { return strings.Contains(e.auditActions(), `"reason":"idle"`) })
}

func TestTerminalLoginSessionEnds(t *testing.T) {
	fs := newTermSys()
	e := newEnv(t, opts{sysHost: fs})
	e.termOn()
	c, _, err := e.dialWS("admin", "?ticket="+e.ticket("admin"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	termWait(t, "session up", func() bool { return e.termSessions() == 1 })
	if code := e.do("admin", "POST", "/api/logout", nil, nil); code != 200 && code != 204 {
		t.Fatalf("logout: %d", code)
	}
	select {
	case <-fs.hangups:
	case <-time.After(15 * time.Second):
		t.Fatal("terminal outlived the login session")
	}
}

func TestTerminalCSPAllowsWebSocket(t *testing.T) {
	e := newEnv(t, opts{})
	_, _, h := e.raw("GET", "/api/health", nil, "", nil)
	csp := h.Get("Content-Security-Policy")
	host := strings.TrimPrefix(e.ts.URL, "http://")
	if !strings.Contains(csp, "connect-src 'self' ws://"+host+" wss://"+host) {
		t.Fatalf("CSP = %q", csp)
	}
}

// drain reads until the server closes the WebSocket, as a browser does; the
// server close handshake waits for the peer to answer.
func drain(c *websocket.Conn) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err := c.Read(ctx); err != nil {
			return
		}
	}
}
