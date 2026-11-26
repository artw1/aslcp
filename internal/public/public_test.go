package public

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var ipLike = regexp.MustCompile(`\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}|[0-9a-f]{1,4}(:[0-9a-f]{0,4}){3,}`)

// memStore is a settings store in memory.
type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) GetSetting(k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.m[k]
	if !ok {
		return "", errNotFound
	}
	return v, nil
}

func (s *memStore) SetSetting(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = map[string]string{}
	}
	s.m[k] = v
	return nil
}

type notFound struct{}

func (notFound) Error() string { return "not found" }

var errNotFound = notFound{}

var t0 = time.Unix(1_800_000_000, 0)

func sample() Input {
	return Input{
		Callsign: "n0call", Mode: "ysf", Target: "Room 1", State: "connected", TxKeyed: true,
		Calling: &CallIn{Mode: "ysf", Call: "N1CALL", Target: "Room 1", Start: t0, Name: "Pat", Place: "Town 192.0.2.7"},
		Heard: []CallIn{
			{Mode: "allstar", Node: "1999", Target: "1998", Start: t0.Add(-time.Minute), End: t0.Add(-50 * time.Second)},
			{Mode: "bm", Call: "1234567", Target: "91", Start: t0.Add(-2 * time.Minute), End: t0.Add(-2*time.Minute + 200*time.Millisecond)},
			{Mode: "nosuch", Call: "N1CALL", Start: t0},
			{Mode: "ysf", Call: "N1CALL", Target: "host.example.net:42000", Start: t0.Add(-3 * time.Minute), End: t0.Add(-2 * time.Minute)},
		},
	}
}

func TestBuild(t *testing.T) {
	cases := []struct {
		name string
		cfg  Settings
		in   Input
		chk  func(t *testing.T, s Snapshot)
	}{
		{"everything", Settings{ShowCallsigns: true, ShowHeard: true}, sample(), func(t *testing.T, s Snapshot) {
			if s.Title != "N0CALL node status" || s.Station.Callsign != "N0CALL" || s.Station.Mode != "ysf" || s.Station.ModeTitle == "" ||
				s.Station.Target != "Room 1" || s.Station.State != "connected" || !s.Station.TxKeyed {
				t.Fatalf("station %+v title %q", s.Station, s.Title)
			}
			if s.Calling == nil || s.Calling.Callsign != "N1CALL" || s.Calling.Name != "Pat" || s.Calling.Location != "Town" || s.Calling.Duration != 0 {
				t.Fatalf("calling %+v", s.Calling)
			}
			if len(s.Heard) != 3 {
				t.Fatalf("heard %+v", s.Heard)
			}
			if h := s.Heard[0]; h.Callsign != "1999" || h.Duration != 10 || h.Target != "1998" {
				t.Errorf("allstar node %+v", h)
			}
			if h := s.Heard[1]; h.Callsign != "" || h.Duration != 1 {
				t.Errorf("a DMR ID is not a callsign: %+v", h)
			}
			if h := s.Heard[2]; h.Target != "" {
				t.Errorf("a host is not published: %+v", h)
			}
		}},
		{"no callsigns", Settings{ShowHeard: true}, sample(), func(t *testing.T, s Snapshot) {
			if s.Calling == nil || s.Calling.Callsign != "" || s.Calling.Name != "" || s.Calling.Location != "" {
				t.Fatalf("calling %+v", s.Calling)
			}
			for _, h := range s.Heard {
				if h.Callsign != "" {
					t.Fatalf("heard %+v", h)
				}
			}
		}},
		{"no heard", Settings{ShowCallsigns: true}, sample(), func(t *testing.T, s Snapshot) {
			if s.Heard != nil {
				t.Fatalf("heard %+v", s.Heard)
			}
		}},
		{"idle", Settings{Title: "My page"}, Input{Mode: "", Target: "1999", State: "connected"}, func(t *testing.T, s Snapshot) {
			if s.Title != "My page" || s.Station.Mode != "" || s.Station.Target != "" || s.Station.State != "" || s.Calling != nil {
				t.Fatalf("%+v", s)
			}
		}},
		{"offline", Settings{}, Input{State: "offline"}, func(t *testing.T, s Snapshot) {
			if s.Station.State != "offline" || s.Station.Mode != "" {
				t.Fatalf("%+v", s)
			}
		}},
		{"default title", Settings{}, Input{}, func(t *testing.T, s Snapshot) {
			if s.Title != "Node status" {
				t.Fatalf("%q", s.Title)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := Build(c.in, c.cfg, t0)
			b, _ := json.Marshal(s)
			if m := ipLike.Find(b); m != nil {
				t.Fatalf("IP-like text %q in %s", m, b)
			}
			if strings.Contains(string(b), "example.net") {
				t.Fatalf("host name in %s", b)
			}
			c.chk(t, s)
		})
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	st := &memStore{}
	def := LoadSettings(st, false)
	if def.Enabled || !def.ShowCallsigns || !def.ShowHeard || def.AllowEmbedding {
		t.Fatalf("defaults %+v", def)
	}
	want := Settings{Enabled: true, Title: "T", ShowHeard: false, ShowCallsigns: false, AllowEmbedding: true, TrustProxy: true}
	if err := SaveSettings(st, want); err != nil {
		t.Fatal(err)
	}
	if got := LoadSettings(st, false); got != want {
		t.Fatalf("got %+v", got)
	}
	bad := []Settings{{Title: strings.Repeat("x", MaxTitle+1)}, {Title: "a\nb"}}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Errorf("%+v accepted", b)
		}
	}
}

func TestStatusGate(t *testing.T) {
	st := &memStore{}
	s := New(Options{Store: st, Input: sample, Now: func() time.Time { return t0 }})
	rec := httptest.NewRecorder()
	s.Status(rec, httptest.NewRequest("GET", "/api/public/status", nil))
	if rec.Code != 404 {
		t.Fatalf("disabled: %d", rec.Code)
	}
	if err := s.Update(Settings{Enabled: true, ShowHeard: true}); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	s.Status(rec, httptest.NewRequest("GET", "/api/public/status", nil))
	var sn Snapshot
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &sn) != nil || sn.Station.Mode != "ysf" || sn.Calling == nil {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	s.Info(rec, httptest.NewRequest("GET", "/api/public/info", nil), true)
	if !strings.Contains(rec.Body.String(), `"enabled":true`) || !strings.Contains(rec.Body.String(), `"loggedIn":true`) {
		t.Fatalf("info %s", rec.Body)
	}
	s.SetRateLimit(0, 1) // no refill: the bucket runs dry
	codes := []int{}
	for i := 0; i < 12; i++ {
		rec = httptest.NewRecorder()
		s.Status(rec, httptest.NewRequest("GET", "/api/public/status", nil))
		codes = append(codes, rec.Code)
	}
	if codes[len(codes)-1] != http.StatusTooManyRequests {
		t.Fatalf("rate limit: %v", codes)
	}
}

func TestClientKey(t *testing.T) {
	cases := []struct {
		remote, xff string
		trust       bool
		want        string
	}{
		{"192.0.2.1:1234", "", false, "192.0.2.1"},
		{"192.0.2.1:1234", "203.0.113.9", false, "192.0.2.1"},
		{"192.0.2.1:1234", "198.51.100.1, 203.0.113.9", true, "203.0.113.9"},
		{"[2001:db8:1:2:3:4:5:6]:1", "", false, "2001:db8:1:2::"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := ClientKey(r, c.trust); got != c.want {
			t.Errorf("%+v: %q", c, got)
		}
	}
}

// Changed sends a snapshot to stream clients only when it differs.
func TestChangedCoalesces(t *testing.T) {
	in := sample()
	var mu sync.Mutex
	s := New(Options{Store: &memStore{}, Input: func() Input { mu.Lock(); defer mu.Unlock(); return in }})
	s.Interval = 0
	if err := s.Update(Settings{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	c, herr := s.hub.add("k")
	if herr != nil {
		t.Fatal(herr.msg)
	}
	s.Changed()
	s.Changed() // same data: nothing new
	mu.Lock()
	in.Target = "Room 2"
	mu.Unlock()
	s.Changed()
	n := 0
	for {
		select {
		case b := <-c.ch:
			if !strings.HasPrefix(string(b), "event: snapshot") {
				t.Fatalf("%s", b)
			}
			n++
			continue
		default:
		}
		break
	}
	if n != 2 {
		t.Fatalf("%d events", n)
	}
}
