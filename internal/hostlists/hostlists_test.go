package hostlists

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParse(t *testing.T) {
	d := ParseDMR(fixture(t, "DMR_Hosts.txt"))
	if len(d) != 2 || d[0].Value != "192.0.2.10" || d[0].Port != 62031 || d[0].Detail != "" || d[1].Detail != "ID 1234" {
		t.Fatalf("dmr: %+v", d)
	}
	y := ParseYSF(fixture(t, "YSF_Hosts.txt"))
	if len(y) != 2 || y[0].Value != "00001" || y[0].Name != "Test Room" || y[0].Port != 42000 {
		t.Fatalf("ysf: %+v", y)
	}
	if p := ParseTG(fixture(t, "P25_Hosts.txt"), "P25"); len(p) != 2 || p[1].Address != "node2001.example.net" {
		t.Fatalf("p25: %+v", p)
	}
	if n := ParseTG(fixture(t, "NXDN_Hosts.txt"), "NXDN"); len(n) != 1 || n[0].Name != "NXDN TG 65000" {
		t.Fatalf("nxdn: %+v", n)
	}
	if got := parse("dmr", []byte("\xef\xbb\xbf")); len(got) != 0 {
		t.Fatal(got)
	}
}

type srv struct {
	hits    int32
	cond    int32
	ua      atomic.Value
	failAll atomic.Bool
}

func (s *srv) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&s.hits, 1)
		s.ua.Store(r.Header.Get("User-Agent"))
		if s.failAll.Load() {
			w.Write([]byte("# nothing usable\n"))
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/dl/")
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("If-None-Match") == `"v1"` {
			atomic.AddInt32(&s.cond, 1)
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Write(b)
	})
}

type set map[string]string

func (s set) GetSetting(k string) (string, error) { return s[k], nil }

func TestRefreshResolveAndKeep(t *testing.T) {
	s := &srv{}
	ts := httptest.NewServer(s.handler(t))
	defer ts.Close()
	dir := t.TempDir()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m := New(Options{Dir: dir, Version: "9.9", Settings: set{SettingBaseURL: ts.URL + "/dl"}, Now: func() time.Time { return now }})
	if _, err := m.Get("ysf"); !errors.Is(err, ErrNoList) {
		t.Fatal(err)
	}
	if err := m.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if ua := s.ua.Load().(string); ua != "ASLCP/9.9" {
		t.Fatal(ua)
	}
	l, err := m.Get("ysf")
	if err != nil || len(l.Items) != 2 || l.ETag == "" || l.Mode != "ysf" {
		t.Fatalf("%+v %v", l, err)
	}
	if tg, ok := m.Resolve("YSF", "00001"); !ok || tg.Host != "192.0.2.20" || tg.Port != 42000 || tg.Room != "00001" {
		t.Fatalf("%+v", tg)
	}
	if tg, ok := m.Resolve("ysf", "Test Room"); !ok || tg.Room != "00001" {
		t.Fatalf("%+v", tg)
	}
	if tg, ok := m.Resolve("ysf", "192.0.2.99:42000"); !ok || tg.Host != "192.0.2.99" || tg.Port != 42000 {
		t.Fatalf("%+v", tg)
	}
	if _, ok := m.Resolve("ysf", "192.0.2.99:042"); ok {
		t.Fatal("leading zero port accepted")
	}
	if _, ok := m.Resolve("p25", "99999"); ok {
		t.Fatal("unknown TG resolved")
	}
	if tg, ok := m.Resolve("nxdn", "65000"); !ok || tg.Port != 41400 {
		t.Fatalf("%+v", tg)
	}
	if _, ok := m.Resolve("dmr", "192.0.2.10"); ok {
		t.Fatal("dmr is not resolved here")
	}
	// too soon, then allowed after ManualEvery
	if err := m.Refresh(context.Background()); !errors.Is(err, ErrTooSoon) {
		t.Fatal(err)
	}
	if st := m.Status(); st.NextManual == "" || st.Lists["dmr"].Entries != 2 {
		t.Fatalf("%+v", st)
	}
	now = now.Add(ManualEvery + time.Second)
	// a bad download keeps the last good copy
	s.failAll.Store(true)
	if err := m.Refresh(context.Background()); err == nil {
		t.Fatal("expected error")
	}
	if l, err := m.Get("ysf"); err != nil || len(l.Items) != 2 {
		t.Fatal("last good copy lost")
	}
	if st := m.Status(); st.Lists["ysf"].Error == "" {
		t.Fatal("error not reported")
	}
	if m2 := entries(t, dir); m2 != 2 {
		t.Fatalf("saved file: %d", m2)
	}
	if ms, _ := filepath.Glob(filepath.Join(dir, ".dl-*")); len(ms) != 0 {
		t.Fatalf("temp files left: %v", ms)
	}
}

func entries(t *testing.T, dir string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "YSF_Hosts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	return len(ParseYSF(b))
}

func TestLoadAndConditional(t *testing.T) {
	s := &srv{}
	ts := httptest.NewServer(s.handler(t))
	defer ts.Close()
	dir := t.TempDir()
	o := Options{Dir: dir, Settings: set{SettingBaseURL: ts.URL + "/dl/"}}
	m := New(o)
	if err := m.refresh(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	// a new manager loads from disk and sends the conditional header
	m2 := New(o)
	m2.Load()
	if _, err := m2.Get("p25"); err != nil {
		t.Fatal(err)
	}
	if err := m2.refresh(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if atomic.LoadInt32(&s.cond) != 4 {
		t.Fatalf("conditional hits: %d", s.cond)
	}
	if _, err := m2.Get("bogus"); err == nil {
		t.Fatal("bad mode accepted")
	}
}

func TestDisabledAndRun(t *testing.T) {
	s := &srv{}
	ts := httptest.NewServer(s.handler(t))
	defer ts.Close()
	var on atomic.Bool
	m := New(Options{Dir: t.TempDir(), Settings: set{SettingBaseURL: ts.URL + "/dl/"}, Enabled: on.Load, FirstDelay: 10 * time.Millisecond})
	if err := m.Refresh(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	time.Sleep(100 * time.Millisecond)
	if atomic.LoadInt32(&s.hits) != 0 {
		t.Fatal("downloaded while disabled")
	}
	cancel()
	<-done
	on.Store(true)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	go m.Run(ctx2)
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := m.Get("dmr"); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("never downloaded")
}

func TestBaseURLInvalidFallsBack(t *testing.T) {
	m := New(Options{Dir: t.TempDir(), Settings: set{SettingBaseURL: "file:///etc"}})
	if m.baseURL() != DefaultBaseURL {
		t.Fatal(m.baseURL())
	}
}
