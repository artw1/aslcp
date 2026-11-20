package api

import (
	"net/http"
	"testing"
	"time"
)

type tclock struct{ t time.Time }

func (c *tclock) now() time.Time        { return c.t }
func (c *tclock) add(d time.Duration)   { c.t = c.t.Add(d) }
func newTClock() *tclock                { return &tclock{t: time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)} }
func newTickets(c *tclock) *ticketStore { return &ticketStore{now: c.now} }

func TestTicketOneTime(t *testing.T) {
	c := newTClock()
	ts := newTickets(c)
	tok, err := ts.issue(7, "sess-a")
	if err != nil || len(tok) < 32 {
		t.Fatalf("%q %v", tok, err)
	}
	if !ts.redeem(tok, 7, "sess-a") {
		t.Fatal("valid ticket refused")
	}
	if ts.redeem(tok, 7, "sess-a") {
		t.Fatal("ticket used twice")
	}
	if ts.redeem("", 7, "sess-a") || ts.redeem("nope", 7, "sess-a") {
		t.Fatal("unknown ticket accepted")
	}
}

func TestTicketExpiry(t *testing.T) {
	c := newTClock()
	ts := newTickets(c)
	tok, _ := ts.issue(1, "s")
	c.add(29 * time.Second)
	if !ts.redeem(tok, 1, "s") {
		t.Fatal("refused inside 30 seconds")
	}
	tok, _ = ts.issue(1, "s")
	c.add(30 * time.Second)
	if ts.redeem(tok, 1, "s") {
		t.Fatal("accepted after 30 seconds")
	}
}

func TestTicketBoundToUserAndSession(t *testing.T) {
	c := newTClock()
	ts := newTickets(c)
	for _, w := range []struct {
		user int64
		sess string
	}{{2, "s"}, {1, "other"}} {
		tok, _ := ts.issue(1, "s")
		if ts.redeem(tok, w.user, w.sess) {
			t.Fatalf("ticket accepted for user %d session %q", w.user, w.sess)
		}
		// a wrong guess burns it
		if ts.redeem(tok, 1, "s") {
			t.Fatal("ticket survived a mismatch")
		}
	}
}

func TestTicketsBoundedAndPurged(t *testing.T) {
	c := newTClock()
	ts := newTickets(c)
	for i := 0; i < maxTickets; i++ {
		if _, err := ts.issue(1, "s"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := ts.issue(1, "s"); err != errTooManyTickets {
		t.Fatalf("err = %v", err)
	}
	c.add(time.Minute) // expired tickets make room
	if _, err := ts.issue(1, "s"); err != nil {
		t.Fatal(err)
	}
	if len(ts.m) != 1 {
		t.Fatalf("%d tickets kept", len(ts.m))
	}
}

func TestTicketNotStoredInClear(t *testing.T) {
	ts := newTickets(newTClock())
	tok, _ := ts.issue(1, "s")
	if _, ok := ts.m[tok]; ok {
		t.Fatal("ticket kept in clear")
	}
}

func TestTerminalExpiry(t *testing.T) {
	start := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	idle, max := 15*time.Minute, 8*time.Hour
	at := func(d time.Duration) time.Time { return start.Add(d) }
	for _, c := range []struct {
		name      string
		now, last time.Duration
		want      string
	}{
		{"fresh", 0, 0, ""},
		{"just under idle", 14*time.Minute + 59*time.Second, 0, ""},
		{"idle", 15 * time.Minute, 0, "idle"},
		{"input resets idle", 20 * time.Minute, 10 * time.Minute, ""},
		{"idle after input", 25 * time.Minute, 10 * time.Minute, "idle"},
		{"hard limit beats activity", 8 * time.Hour, 8*time.Hour - time.Second, "limit"},
		{"under hard limit", 8*time.Hour - time.Second, 8*time.Hour - time.Second, ""},
	} {
		if got := terminalExpiry(at(c.now), start, at(c.last), idle, max); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
}

func TestTerminalOriginOK(t *testing.T) {
	mk := func(host, origin string) *http.Request {
		r, _ := http.NewRequest("GET", "http://"+host+"/api/system/terminal/ws", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	extra := []string{"http://localhost:5173"}
	for _, c := range []struct {
		host, origin string
		ok           bool
	}{
		{"node2001.local:8080", "http://node2001.local:8080", true},
		{"node2001.local:8080", "https://NODE2001.local:8080", true},
		{"node2001.local:8080", "", false}, // a handshake always has one
		{"node2001.local:8080", "https://attacker.example", false},
		{"node2001.local:8080", "http://node2001.local:9999", false},
		{"node2001.local:8080", "null", false},
		{"node2001.local:8080", "http://localhost:5173", true},
		{"node2001.local:8080", "http://localhost:5174", false},
	} {
		if got := terminalOriginOK(mk(c.host, c.origin), extra); got != c.ok {
			t.Errorf("host %q origin %q: got %v", c.host, c.origin, got)
		}
	}
}

func TestCSPHost(t *testing.T) {
	for in, want := range map[string]string{
		"node2001.local:8080": "node2001.local:8080",
		"192.0.2.10":          "192.0.2.10",
		"[2001:db8::10]:8080": "[2001:db8::10]:8080",
		"":                    "",
		"a b":                 "",
		"x;script-src *":      "",
		"x'y":                 "",
	} {
		if got := cspHost(in); got != want {
			t.Errorf("cspHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCleanLinuxUser(t *testing.T) {
	for in, want := range map[string]string{"alice": "alice", "pi": "pi", "svc-x.1": "svc-x.1", "": "", "a b": "", "x\n": "", "a\"b": ""} {
		if got := cleanLinuxUser([]byte(in)); got != want {
			t.Errorf("cleanLinuxUser(%q) = %q, want %q", in, got, want)
		}
	}
	if cleanLinuxUser(make([]byte, 65)) != "" {
		t.Error("overlong name accepted")
	}
}

func TestTermStateLimit(t *testing.T) {
	var ts termState
	ts.maxLive = 2
	a, ok1 := ts.reserve(func() {}, 0, 0)
	_, ok2 := ts.reserve(func() {}, 0, 0)
	_, ok3 := ts.reserve(func() {}, 0, 0)
	if !ok1 || !ok2 || ok3 {
		t.Fatalf("%v %v %v", ok1, ok2, ok3)
	}
	ts.release(a)
	if _, ok := ts.reserve(func() {}, 0, 0); !ok {
		t.Fatal("slot not freed")
	}
	// endAll cancels everyone with the reason
	var got []string
	var s2 termState
	s2.reserve(func() { got = append(got, "x") }, 0, 0)
	s2.endAll("disabled")
	if len(got) != 1 {
		t.Fatalf("%v", got)
	}
}
