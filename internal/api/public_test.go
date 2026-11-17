package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/public"
	"github.com/artw1/aslcp/internal/store"
)

var ipLike = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}`)

func enablePublic(t *testing.T, e *env, patch map[string]any) {
	t.Helper()
	if patch == nil {
		patch = map[string]any{}
	}
	patch["enabled"] = true
	if code := e.do("admin", "PUT", "/api/public", patch, nil); code != 200 {
		t.Fatalf("enable: %d", code)
	}
}

// openPublic opens /api/public/events; the stream is nil for a non-200 status.
func openPublic(t *testing.T, e *env, hdr map[string]string) (*sseStream, int) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "GET", e.ts.URL+"/api/public/events", nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	if resp.StatusCode != 200 {
		resp.Body.Close()
		cancel()
		return nil, resp.StatusCode
	}
	s := &sseStream{events: make(chan sseEvent, 1000), pings: make(chan struct{}, 1000), cancel: cancel, closed: make(chan struct{}), resp: resp, t: t}
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
	t.Cleanup(cancel)
	return s, 200
}

func TestPublicDisabledByDefault(t *testing.T) {
	e := newEnv(t, opts{})
	var info struct {
		Enabled  bool
		Title    string
		LoggedIn bool
	}
	if code := e.do("", "GET", "/api/public/info", nil, &info); code != 200 || info.Enabled || info.LoggedIn {
		t.Fatalf("%d %+v", code, info)
	}
	var adm struct{ LoggedIn bool }
	if code := e.do("admin", "GET", "/api/public/info", nil, &adm); code != 200 || !adm.LoggedIn {
		t.Fatalf("%d %+v", code, adm)
	}
	expectErr(t, e, "", "GET", "/api/public/status", nil, 404, "not_found")
	// public endpoints are GET only (anything else falls through to the catch-all)
	if code, _, _ := e.raw("POST", "/api/public/status", nil, "", nil); code != 404 && code != 405 {
		t.Fatalf("POST: %d", code)
	}
	if _, code := openPublic(t, e, nil); code != 404 {
		t.Fatalf("events while disabled: %d", code)
	}
	enablePublic(t, e, nil)
	if code := e.do("", "GET", "/api/public/info", nil, &info); code != 200 || !info.Enabled || !strings.Contains(info.Title, "AllStarLink") {
		t.Fatalf("%d %+v", code, info)
	}
	if code := e.do("", "GET", "/api/public/status", nil, nil); code != 200 {
		t.Fatal(code)
	}
	// disabling again closes the door (and open streams)
	s, _ := openPublic(t, e, nil)
	s.first()
	if code := e.do("admin", "PUT", "/api/public", map[string]any{"enabled": false}, nil); code != 200 {
		t.Fatal(code)
	}
	select {
	case <-s.closed:
	case <-time.After(3 * time.Second):
		t.Fatal("stream not closed on disable")
	}
	expectErr(t, e, "", "GET", "/api/public/status", nil, 404, "not_found")
}

func TestPublicDoesNotOpenOtherAPIRoutes(t *testing.T) {
	e := newEnv(t, opts{})
	enablePublic(t, e, nil)
	for _, p := range []string{"/api/nodes", "/api/favorites", "/api/settings", "/api/system", "/api/public", "/api/activity",
		"/api/events", "/api/users", "/api/about", "/api/lookup/2000", "/api/lookup?q=a", "/api/nodelist", "/api/updates",
		"/api/diagnostics", "/api/nodes/1999/stats", "/api/session", "/api/favorites/stats"} {
		if code := e.do("", "GET", p, nil, nil); code != 401 {
			t.Errorf("anonymous GET %s: %d, want 401", p, code)
		}
	}
	// the admin-only settings stay admin-only
	for _, role := range []string{"op", "viewer"} {
		if code := e.do(role, "GET", "/api/public", nil, nil); code != 403 {
			t.Errorf("%s GET /api/public: %d", role, code)
		}
		if code := e.do(role, "PUT", "/api/public", map[string]any{"enabled": false}, nil); code != 403 {
			t.Errorf("%s PUT /api/public: %d", role, code)
		}
	}
	if code := e.do("", "PUT", "/api/public", map[string]any{"enabled": false}, nil); code != 401 {
		t.Errorf("anonymous PUT: %d", code)
	}
}

func TestPublicDataHasNoIPsOrSystemInfo(t *testing.T) {
	e := newEnv(t, opts{})
	enablePublic(t, e, nil)
	// sanity: the admin view of the same node does contain the link IP
	if _, b, _ := e.raw("GET", "/api/nodes", nil, e.ck["admin"], nil); !ipLike.Match(b) {
		t.Fatalf("test premise: admin JSON should contain an IP: %s", b)
	}
	_, body, hdr := e.raw("GET", "/api/public/status", nil, "", nil)
	if ipLike.Match(body) || strings.Contains(strings.ToLower(string(body)), `"ip"`) {
		t.Fatalf("IP in public status: %s", body)
	}
	for _, k := range []string{"hostname", "version", "load", "cpuTemp", "asterisk", "flags", "secret", "favorites"} {
		if strings.Contains(string(body), k) {
			t.Errorf("public status mentions %q: %s", k, body)
		}
	}
	if hdr.Get("Cache-Control") != "no-store" || !strings.HasPrefix(hdr.Get("Content-Type"), "application/json") {
		t.Errorf("headers %v", hdr)
	}
	if c := hdr.Get("Set-Cookie"); c != "" {
		t.Errorf("cookie set: %q", c)
	}
	var snap public.Snapshot
	if err := json.Unmarshal(body, &snap); err != nil || len(snap.Nodes) != 1 || snap.Nodes[0].Node != localNode {
		t.Fatalf("%v %s", err, body)
	}
	n := snap.Nodes[0]
	if n.Info == nil || n.Info.Callsign != "N0AAD" || len(n.Links) != 1 || n.Links[0].Node != "50241" || n.Links[0].Info == nil || n.Links[0].Info.Callsign != "N0AAC" {
		t.Fatalf("%+v", n)
	}
	if !strings.Contains(snap.Title, "N0AAD AllStarLink node "+localNode) {
		t.Fatalf("title %q", snap.Title)
	}

	// SSE: snapshot and changes carry no IPs either
	s, code := openPublic(t, e, nil)
	if code != 200 {
		t.Fatal(code)
	}
	ev := s.first()
	if ev.name != "snapshot" || ipLike.MatchString(ev.data) {
		t.Fatalf("%s %s", ev.name, ev.data)
	}
	e.fake.setRx(true)
	ev = s.wait(func(ev sseEvent) bool { return ev.name == "node" && strings.Contains(ev.data, `"rxKeyed":true`) })
	if ipLike.MatchString(ev.data) || strings.Contains(ev.data, `"network"`) {
		t.Fatalf("node event: %s", ev.data)
	}
	if got := s.resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Fatal(got)
	}
}

func TestPublicCallsignHidingAndTitle(t *testing.T) {
	e := newEnv(t, opts{})
	enablePublic(t, e, map[string]any{"showCallsigns": false, "title": "Club <b>node</b>"})
	var snap public.Snapshot
	e.do("", "GET", "/api/public/status", nil, &snap)
	if snap.Callsigns || snap.Title != "Club <b>node</b>" {
		t.Fatalf("%+v", snap)
	}
	n := snap.Nodes[0]
	if n.Links[0].Info != nil || n.Links[0].Kind == "" || n.Links[0].Node != "50241" {
		t.Fatalf("link info must be hidden: %+v", n.Links[0])
	}
	for _, nn := range n.Network {
		if nn.Callsign != "" {
			t.Fatalf("network callsign shown: %+v", nn)
		}
	}
	_, raw, _ := e.raw("GET", "/api/public/status", nil, "", nil)
	if strings.Contains(string(raw), "N0AAC") || strings.Contains(string(raw), "Metro Hub") {
		t.Fatalf("link identity leaked: %s", raw)
	}
	// network and activity can be switched off
	e.do("admin", "PUT", "/api/public", map[string]any{"showNetwork": false, "showActivity": false}, nil)
	_, raw, _ = e.raw("GET", "/api/public/status", nil, "", nil)
	if strings.Contains(string(raw), `"network"`) || strings.Contains(string(raw), `"activity"`) {
		t.Fatalf("%s", raw)
	}
}

func TestPublicSettingsValidation(t *testing.T) {
	e := newEnv(t, opts{})
	expectErr(t, e, "admin", "PUT", "/api/public", map[string]any{"title": strings.Repeat("x", 81)}, 400, "bad_request")
	expectErr(t, e, "admin", "PUT", "/api/public", map[string]any{"title": "a\x00b"}, 400, "bad_request")
	expectErr(t, e, "admin", "PUT", "/api/public", map[string]any{"nodes": []string{"abc"}}, 400, "bad_request")
	expectErr(t, e, "admin", "PUT", "/api/public", map[string]any{"nodes": []string{"4242"}}, 400, "bad_request")
	var out struct {
		Nodes      []string
		LocalNodes []struct{ Node string }
	}
	if code := e.do("admin", "PUT", "/api/public", map[string]any{"nodes": []string{localNode}, "trustProxy": true}, &out); code != 200 {
		t.Fatal(code)
	}
	if len(out.Nodes) != 0 || len(out.LocalNodes) != 1 { // selecting every local node means "all"
		t.Fatalf("%+v", out)
	}
	// persisted in the settings table
	if v, err := e.st.GetSetting("public.trust_proxy"); err != nil || v != "true" {
		t.Fatalf("%q %v", v, err)
	}
}

func TestPublicSSELimits(t *testing.T) {
	e := newEnv(t, opts{})
	enablePublic(t, e, nil)
	e.srv.Public().MaxPerIP = 2
	e.srv.Public().MaxClients = 3
	e.srv.Public().SetRateLimit(1000, 1000)

	s1, c := openPublic(t, e, nil)
	s2, c2 := openPublic(t, e, nil)
	if c != 200 || c2 != 200 {
		t.Fatal(c, c2)
	}
	s1.first()
	s2.first()
	if _, c := openPublic(t, e, nil); c != 429 {
		t.Fatalf("third connection from one address: %d, want 429", c)
	}
	// Without trust-proxy the header is ignored: still the same client.
	if _, c := openPublic(t, e, map[string]string{"X-Forwarded-For": "203.0.113.7"}); c != 429 {
		t.Fatalf("spoofed X-Forwarded-For must not help: %d", c)
	}
	// With trust-proxy it identifies the client, and the global cap applies.
	e.do("admin", "PUT", "/api/public", map[string]any{"trustProxy": true}, nil)
	s3, c := openPublic(t, e, map[string]string{"X-Forwarded-For": "198.51.100.1, 203.0.113.7"})
	if c != 200 {
		t.Fatalf("other client behind the proxy: %d", c)
	}
	s3.first()
	if _, c := openPublic(t, e, map[string]string{"X-Forwarded-For": "198.51.100.2"}); c != 503 {
		t.Fatalf("global cap: %d, want 503", c)
	}
	// closing one frees a slot
	s1.cancel()
	deadline := time.Now().Add(3 * time.Second)
	for e.srv.Public().Clients() > 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if _, c := openPublic(t, e, map[string]string{"X-Forwarded-For": "198.51.100.2"}); c != 200 {
		t.Fatalf("after a disconnect: %d", c)
	}
}

func TestPublicRateLimit(t *testing.T) {
	e := newEnv(t, opts{})
	enablePublic(t, e, nil)
	e.srv.Public().SetRateLimit(0.001, 3)
	got := map[int]int{}
	for i := 0; i < 8; i++ {
		code, body, hdr := e.raw("GET", "/api/public/status", nil, "", nil)
		got[code]++
		if code == 429 && (hdr.Get("Retry-After") == "" || !strings.Contains(string(body), "rate_limited")) {
			t.Fatalf("%v %s", hdr, body)
		}
	}
	if got[200] != 3 || got[429] != 5 {
		t.Fatalf("%v", got)
	}
	// the admin API is not subject to it
	if code := e.do("admin", "GET", "/api/nodes", nil, nil); code != 200 {
		t.Fatal(code)
	}
}

func TestPublicFastPollingWhileWatched(t *testing.T) {
	e := newEnv(t, opts{})
	enablePublic(t, e, nil)
	e.srv.Public().SetRateLimit(1000, 1000)
	waitInterval := func(want time.Duration) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for e.mon.Interval() != want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if e.mon.Interval() != want {
			t.Fatalf("interval %v, want %v", e.mon.Interval(), want)
		}
	}
	waitInterval(e.srv.SlowInterval)
	s, _ := openPublic(t, e, nil)
	s.first()
	waitInterval(e.srv.FastInterval)
	s.cancel()
	waitInterval(e.srv.SlowInterval)
}

func TestEmbedFramingHeaders(t *testing.T) {
	e := newEnv(t, opts{})
	fa := func(path string) (string, string) {
		_, _, h := e.raw("GET", path, nil, "", nil)
		return h.Get("Content-Security-Policy"), h.Get("X-Frame-Options")
	}
	has := func(csp, want string) bool { return strings.Contains(csp, "frame-ancestors "+want+";") }
	// default: nothing can be framed
	for _, p := range []string{"/", "/embed", "/login", "/api/public/info"} {
		if csp, xfo := fa(p); !has(csp, "'none'") || xfo != "DENY" {
			t.Fatalf("%s: %q %q", p, csp, xfo)
		}
	}
	// embedding on but the public page off: still not framable
	e.do("admin", "PUT", "/api/public", map[string]any{"allowEmbedding": true}, nil)
	if csp, _ := fa("/embed"); !has(csp, "'none'") {
		t.Fatalf("embed while public is disabled: %q", csp)
	}
	enablePublic(t, e, nil)
	csp, xfo := fa("/embed")
	if !has(csp, "*") || xfo != "" {
		t.Fatalf("/embed: %q %q", csp, xfo)
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatal(csp)
	}
	// everything else, the login page included, stays unframable
	for _, p := range []string{"/", "/embedded", "/login", "/index.html", "/api/public/status", "/api/session"} {
		if csp, xfo := fa(p); !has(csp, "'none'") || xfo != "DENY" {
			t.Fatalf("%s: %q %q", p, csp, xfo)
		}
	}
	e.do("admin", "PUT", "/api/public", map[string]any{"allowEmbedding": false}, nil)
	if csp, _ := fa("/embed"); !has(csp, "'none'") {
		t.Fatalf("embed after switching it off: %q", csp)
	}
}

func TestActivityAPI(t *testing.T) {
	e := newEnv(t, opts{})
	now := time.Now()
	for _, a := range []struct {
		link string
		ago  time.Duration
		d    time.Duration
	}{{"50241", time.Hour, 5 * time.Second}, {"local", time.Minute, 1200 * time.Millisecond}} {
		if err := e.st.AddActivity(storeActivity(localNode, a.link, now.Add(-a.ago), a.d)); err != nil {
			t.Fatal(err)
		}
	}
	var got []struct {
		Node, Link, Start string
		Info              *struct{ Callsign string }
		Duration          float64
		Kerchunk          bool
	}
	if code := e.do("viewer", "GET", "/api/activity?node="+localNode+"&limit=5", nil, &got); code != 200 || len(got) != 2 {
		t.Fatalf("%d %+v", code, got)
	}
	if got[0].Link != "local" || !got[0].Kerchunk || got[0].Duration != 1.2 || got[1].Info == nil || got[1].Info.Callsign != "N0AAC" {
		t.Fatalf("%+v", got)
	}
	expectErr(t, e, "", "GET", "/api/activity", nil, 401, "unauthorized")
	expectErr(t, e, "admin", "GET", "/api/activity?limit=0", nil, 400, "bad_request")
	expectErr(t, e, "admin", "GET", "/api/activity?node=x", nil, 400, "bad_request")

	// the public page shows it, with callsigns only when allowed
	enablePublic(t, e, nil)
	var snap public.Snapshot
	e.do("", "GET", "/api/public/status", nil, &snap)
	if len(snap.Activity) != 2 || snap.Activity[1].Callsign != "N0AAC" || snap.Activity[0].Duration != 1 {
		t.Fatalf("%+v", snap.Activity)
	}
	e.do("admin", "PUT", "/api/public", map[string]any{"showCallsigns": false}, nil)
	var hidden public.Snapshot
	e.do("", "GET", "/api/public/status", nil, &hidden)
	if len(hidden.Activity) != 2 || hidden.Activity[1].Callsign != "" {
		t.Fatalf("%+v", hidden.Activity)
	}
}

func storeActivity(node, link string, start time.Time, d time.Duration) store.Activity {
	return store.Activity{Node: node, Link: link, Start: start, Duration: d, Kerchunk: d < 1500*time.Millisecond}
}
