package register

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []chan time.Time
	delays  []time.Duration
	armed   chan struct{}
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Unix(1000, 0), armed: make(chan struct{}, 100)}
}
func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	c.waiters = append(c.waiters, ch)
	c.delays = append(c.delays, d)
	c.armed <- struct{}{}
	return ch
}

// fire waits for the loop to arm a timer, advances time and releases it.
func (c *fakeClock) fire(t *testing.T) time.Duration {
	t.Helper()
	select {
	case <-c.armed:
	case <-time.After(5 * time.Second):
		t.Fatal("loop never armed a timer")
	}
	c.mu.Lock()
	d := c.delays[len(c.delays)-1]
	ch := c.waiters[len(c.waiters)-1]
	c.now = c.now.Add(d)
	c.mu.Unlock()
	ch <- c.now
	return d
}

func waitAttempts(t *testing.T, r *Registrar, n int, srv *fakeServer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for srv.count() < n {
		if time.Now().After(deadline) {
			t.Fatalf("want %d requests, got %d", n, srv.count())
		}
		time.Sleep(time.Millisecond)
	}
	for r.Status().LastAttempt.IsZero() {
		time.Sleep(time.Millisecond)
	}
}

type fakeServer struct {
	*httptest.Server
	mu     sync.Mutex
	n      int
	bodies []string
	handle func(n int, w http.ResponseWriter)
	ua     string
}

func (s *fakeServer) count() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }

func newServer(h func(n int, w http.ResponseWriter)) *fakeServer {
	s := &fakeServer{handle: h}
	s.Server = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		s.mu.Lock()
		s.n++
		n := s.n
		s.bodies = append(s.bodies, string(b))
		s.ua = req.Header.Get("User-Agent")
		s.mu.Unlock()
		s.handle(n, w)
	}))
	return s
}

const okReply = `{"ipaddr":"203.0.113.7","port":4569,"refresh":60,"data":"Node 1997 successfully registered"}`

func okHandler(_ int, w http.ResponseWriter) { io.WriteString(w, okReply) }

func newReg(t *testing.T, s *fakeServer, c *fakeClock, mod func(*Config)) *Registrar {
	t.Helper()
	cfg := Config{Node: "1997", Password: "s3cretpw", Port: 4569, URL: s.URL, Clock: c, HTTPClient: s.Client()}
	if mod != nil {
		mod(&cfg)
	}
	r, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestSuccessAndBody(t *testing.T) {
	s := newServer(okHandler)
	defer s.Close()
	c := newClock()
	r := newReg(t, s, c, nil)
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	waitAttempts(t, r, 1, s)
	<-c.armed
	c.armed <- struct{}{}
	st := r.Status()
	if !st.Registered || st.PublicIP != "203.0.113.7" || st.LastError != "" || st.HTTPStatus != 200 || st.LastSuccess.IsZero() {
		t.Fatalf("bad status %+v", st)
	}
	var got regBody
	if err := json.Unmarshal([]byte(s.bodies[0]), &got); err != nil {
		t.Fatal(err)
	}
	e := got.Data.Nodes["1997"]
	if got.Port != 4569 || e.Node != "1997" || e.Passwd != "s3cretpw" || e.Remote != 0 {
		t.Fatalf("bad body %s", s.bodies[0])
	}
	if s.ua != userAgent {
		t.Fatalf("ua %q", s.ua)
	}
}

func TestFailures(t *testing.T) {
	tests := []struct {
		name    string
		handler func(int, http.ResponseWriter)
		wantErr string
		code    int
	}{
		{"wrong password", func(_ int, w http.ResponseWriter) {
			io.WriteString(w, `{"ipaddr":"203.0.113.7","data":"authentication failed"}`)
		}, ErrNotRegistered.Error(), 200},
		{"server error", func(_ int, w http.ResponseWriter) { w.WriteHeader(503) }, "HTTP 503", 503},
		{"not json", func(_ int, w http.ResponseWriter) { io.WriteString(w, "<html>") }, "decode reply", 200},
		{"redirect not followed", func(_ int, w http.ResponseWriter) { w.Header().Set("Location", "/x"); w.WriteHeader(302) }, "HTTP 302", 302},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newServer(tt.handler)
			defer s.Close()
			c := newClock()
			hc := s.Client()
			hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
			r := newReg(t, s, c, func(cf *Config) { cf.HTTPClient = hc })
			r.Start(context.Background())
			defer r.Stop()
			waitAttempts(t, r, 1, s)
			<-c.armed
			c.armed <- struct{}{}
			st := r.Status()
			if st.Registered || !strings.Contains(st.LastError, tt.wantErr) || st.HTTPStatus != tt.code || st.Failures != 1 {
				t.Fatalf("status %+v", st)
			}
			if strings.Contains(st.LastError, "s3cretpw") {
				t.Fatal("password leaked")
			}
			if r.Ready() {
				t.Fatal("ready while failing")
			}
		})
	}
}

func TestTimingAndBackoff(t *testing.T) {
	s := newServer(func(n int, w http.ResponseWriter) {
		if n >= 2 && n <= 5 {
			w.WriteHeader(500)
			return
		}
		okHandler(n, w)
	})
	defer s.Close()
	c := newClock()
	r := newReg(t, s, c, func(cf *Config) { cf.Interval = 60 * time.Second; cf.MaxBackoff = 200 * time.Second })
	r.Start(context.Background())
	defer r.Stop()
	want := []time.Duration{61 * time.Second, 60 * time.Second, 120 * time.Second, 200 * time.Second, 200 * time.Second, 61 * time.Second}
	for i, w := range want {
		waitAttempts(t, r, i+1, s)
		if d := c.fire(t); d != w {
			t.Fatalf("delay %d = %v, want %v", i, d, w)
		}
		if i == len(want)-1 {
			break
		}
		// wait for next request before checking next delay
		waitAttempts(t, r, i+2, s)
		c.armed <- struct{}{} // re-arm marker consumed by fire
		<-c.armed
		// fire consumed marker; put back so next fire sees it
		c.armed <- struct{}{}
	}
	waitAttempts(t, r, 7, s)
	for !r.Status().Registered {
		time.Sleep(time.Millisecond)
	}
	if r.Status().Failures != 0 {
		t.Fatal("failures not reset")
	}
}

func TestDNSCheck(t *testing.T) {
	s := newServer(okHandler)
	defer s.Close()
	c := newClock()
	var mu sync.Mutex
	addrs := []string{}
	r := newReg(t, s, c, func(cf *Config) {
		cf.LookupHost = func(_ context.Context, name string) ([]string, error) {
			mu.Lock()
			defer mu.Unlock()
			if name != "1997.nodes.allstarlink.org" {
				t.Errorf("name %q", name)
			}
			return addrs, nil
		}
	})
	r.Start(context.Background())
	defer r.Stop()
	waitAttempts(t, r, 1, s)
	<-c.armed
	c.armed <- struct{}{}
	if st := r.Status(); !st.DNSChecked || st.DNSOK || r.Ready() {
		t.Fatalf("dns should not match yet: %+v", st)
	}
	mu.Lock()
	addrs = []string{"203.0.113.7"}
	mu.Unlock()
	c.fire(t)
	waitAttempts(t, r, 2, s)
	deadline := time.Now().Add(5 * time.Second)
	for !r.Ready() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !r.Ready() {
		t.Fatalf("not ready: %+v", r.Status())
	}
}

func TestStop(t *testing.T) {
	s := newServer(okHandler)
	defer s.Close()
	c := newClock()
	r := newReg(t, s, c, nil)
	r.Start(context.Background())
	waitAttempts(t, r, 1, s)
	<-c.armed
	done := make(chan struct{})
	go func() { r.Stop(); r.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop hung")
	}
	if s.count() != 1 {
		t.Fatalf("requests after stop: %d", s.count())
	}
	if err := r.Start(context.Background()); err == nil {
		t.Fatal("second start should fail")
	}
}

func TestNewValidation(t *testing.T) {
	for _, cfg := range []Config{{Password: "x"}, {Node: "1997"}} {
		if _, err := New(cfg); err == nil {
			t.Errorf("%+v: want error", cfg)
		}
	}
}
