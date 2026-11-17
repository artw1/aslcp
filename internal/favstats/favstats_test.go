package favstats

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

// Anonymized fixture: string counters, boolean keyed, short linkedNodes entries.
const fixture = `{"stats":{"id":1,"node":50001,"data":{
 "apprptuptime":"1047840","totalexecdcommands":"92","totalkeyups":"14834",
 "totaltxtime":"358786","links":["50002","50003","50004"],"keyed":false,
 "nodes":"T50002,T50003,T50004",
 "linkedNodes":[{"name":50002,"Status":"Active","callsign":"N0CALL","server":{"Location":"x"}},{"name":"3355800"}]}},
 "node":{"Status":"Active","access_webtransceiver":"1","callsign":"N0CALL"}}`

type clock struct{ t time.Time }

func (c *clock) now() time.Time      { return c.t }
func (c *clock) add(d time.Duration) { c.t = c.t.Add(d) }
func quiet() *log.Logger             { return log.New(io.Discard, "", 0) }
func ptr(i int) *int                 { return &i }
func newClock() *clock               { return &clock{t: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }

type fakeAPI struct {
	mu    sync.Mutex
	hits  []string
	reply func(node string) (int, string)
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	node := strings.TrimPrefix(r.URL.Path, "/api/stats/")
	f.mu.Lock()
	f.hits = append(f.hits, node)
	f.mu.Unlock()
	code, body := 200, fixture
	if f.reply != nil {
		code, body = f.reply(node)
	}
	if code == 429 {
		w.Header().Set("Retry-After", "1")
	}
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

func (f *fakeAPI) count() int { f.mu.Lock(); defer f.mu.Unlock(); return len(f.hits) }

func setup(t *testing.T, fa *fakeAPI, favs ...string) (*Service, *clock) {
	ts := httptest.NewServer(fa)
	t.Cleanup(ts.Close)
	ck := newClock()
	s := New(Options{BaseURL: ts.URL, Logger: quiet(), Now: ck.now})
	s.Favorites = func() []string { return favs }
	s.Local = func() []string { return []string{"1999"} }
	s.SetRate(10) // the tests below assume a 6 s gap; the default is checked in TestDefaultRate
	s.SetEnabled(true)
	s.SetViewers(1)
	return s, ck
}

func TestDecodeFixture(t *testing.T) {
	smp, err := Decode(strings.NewReader(fixture))
	if err != nil {
		t.Fatal(err)
	}
	if smp.Uptime != 1047840 || smp.TxTime != 358786 || smp.KeyUps != 14834 || !smp.HaveCounters || smp.Keyed {
		t.Fatalf("%+v", smp)
	}
	if smp.Links != 3 || !smp.Active || !smp.WebTx {
		t.Fatalf("%+v", smp)
	}
	if v := RxPct(smp.Uptime, smp.TxTime); v == nil || *v != 34 {
		t.Fatalf("rx %v", v)
	}
}

func TestDecodeTolerant(t *testing.T) {
	// Numbers instead of strings, numeric keyed, no top-level node object.
	smp, err := Decode(strings.NewReader(`{"stats":{"node":5,"data":{"apprptuptime":100,"totaltxtime":20,"totalkeyups":3,"keyed":1,"links":[]}},"node":5}`))
	if err != nil || smp.Uptime != 100 || smp.TxTime != 20 || !smp.Keyed || smp.Active || smp.WebTx || smp.Links != 0 {
		t.Fatalf("%+v %v", smp, err)
	}
	if _, err := Decode(strings.NewReader(`{"stats":{}}`)); err == nil {
		t.Fatal("empty response accepted")
	}
	if _, err := Decode(strings.NewReader(`<html>`)); err == nil {
		t.Fatal("html accepted")
	}
}

func TestRxPctEdges(t *testing.T) {
	cases := []struct {
		up, tx float64
		want   *int
	}{
		{60, 100, nil}, {61, 10, nil}, {61, 10.5, ptr(17)}, {1000, 0, nil}, {1000, 500, ptr(50)}, {1000, 5, nil}, {3, 3, nil}, {1000, 1000, ptr(100)},
	}
	for _, c := range cases {
		got := RxPct(c.up, c.tx)
		if (got == nil) != (c.want == nil) || (got != nil && *got != *c.want) {
			t.Errorf("RxPct(%v,%v)=%v want %v", c.up, c.tx, got, c.want)
		}
	}
}

func TestValidNode(t *testing.T) {
	for n, want := range map[string]bool{"2000": true, "1999": false, "999999": true, "1000000": false, "3000000": false, "3999999": false, "4000000": false, "0123456": false, "abc": false, "": false, "50001": true, "99999999": false} {
		if ValidNode(n) != want {
			t.Errorf("ValidNode(%q) != %v", n, want)
		}
	}
}

func TestKeyedDeltaAndReset(t *testing.T) {
	s := New(Options{Logger: quiet(), Now: newClock().now})
	t0 := newClock().t
	smp := func(tx, ku float64) Sample {
		return Sample{Uptime: 100000, TxTime: tx, KeyUps: ku, HaveCounters: true}
	}
	a := s.compute("50001", smp(1000, 100), t0)
	if a.Keyed || a.TxAvg != 0 {
		t.Fatalf("first: %+v", a)
	}
	a = s.compute("50001", smp(1000, 100), t0.Add(10*time.Second))
	if a.Keyed || a.TxAvg != 0 {
		t.Fatalf("idle: %+v", a)
	}
	a = s.compute("50001", smp(1005, 101), t0.Add(20*time.Second))
	if !a.Keyed || a.TxAvg != 100 {
		t.Fatalf("keyed: %+v", a)
	}
	a = s.compute("50001", smp(1005, 101), t0.Add(30*time.Second))
	if a.Keyed || a.TxAvg != 50 {
		t.Fatalf("decay: %+v", a)
	}
	// Counter reset (node restarted): no activity, no panic.
	a = s.compute("50001", smp(3, 1), t0.Add(40*time.Second))
	if a.Keyed || a.TxAvg != 25 {
		t.Fatalf("reset: %+v", a)
	}
	// Implausible jump (more TX seconds than elapsed).
	a = s.compute("50001", smp(900000, 5000), t0.Add(50*time.Second))
	if a.Keyed {
		t.Fatalf("jump: %+v", a)
	}
}

func TestKeyedFirstSampleUsesReported(t *testing.T) {
	s := New(Options{Logger: quiet(), Now: newClock().now})
	a := s.compute("50001", Sample{Uptime: 1, HaveCounters: true, Keyed: true}, newClock().t)
	if !a.Keyed || a.TxAvg != 100 {
		t.Fatalf("%+v", a)
	}
}

func TestPollRoundRobinSkipsLocalAndEchoLink(t *testing.T) {
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001", "1999", "3500000", "4000001", "1234", "999999", "50002", "50001", "abc")
	ctx := context.Background()
	for i := 0; i < 8; i++ {
		s.Tick(ctx)
		ck.add(6 * time.Second)
	}
	// 50001, 999999 (highest regular number), 50002 only; local 1999, EchoLink
	// (3500000) and other out-of-range numbers are skipped.
	want := []string{"50001", "999999", "50002", "50001", "999999", "50002", "50001", "999999"}
	if strings.Join(fa.hits, ",") != strings.Join(want, ",") {
		t.Fatalf("hits %v", fa.hits)
	}
	snap := s.Snapshot()
	a, ok := snap.Stats["50002"]
	if !ok || a.LinkCnt != 3 || !a.Active || !a.WebTx || a.RxPct == nil || *a.RxPct != 34 {
		t.Fatalf("%+v", snap)
	}
	if _, ok := snap.Stats["1999"]; ok {
		t.Fatal("local node polled")
	}
}

func TestDisabledNoRequests(t *testing.T) {
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001")
	s.SetEnabled(false)
	for i := 0; i < 5; i++ {
		s.Tick(context.Background())
		ck.add(10 * time.Second)
	}
	if fa.count() != 0 {
		t.Fatalf("%d requests while disabled", fa.count())
	}
}

func TestViewersStartStopAndKeep(t *testing.T) {
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001")
	ctx := context.Background()
	s.SetViewers(0)
	s.Tick(ctx)
	if fa.count() != 0 {
		t.Fatal("polled with no viewer")
	}
	s.SetViewers(2)
	s.Tick(ctx)
	if fa.count() != 1 {
		t.Fatal("no poll with viewer")
	}
	s.SetViewers(0)
	ck.add(9 * time.Minute)
	s.Tick(ctx)
	if fa.count() != 1 || len(s.Snapshot().Stats) != 1 {
		t.Fatal("values lost or polled early")
	}
	ck.add(2 * time.Minute)
	if len(s.Snapshot().Stats) != 0 {
		t.Fatal("values kept after 10 minutes")
	}
}

// reqTimes ticks every 250 ms for d and returns the clock offsets at which
// a request was made.
func reqTimes(s *Service, ck *clock, d time.Duration) []time.Duration {
	start := ck.t
	var at []time.Duration
	n := 0
	s.fetch = wrapFetch(s.fetch, func() { at = append(at, ck.t.Sub(start)); n++ })
	for ck.t.Sub(start) <= d {
		s.Tick(context.Background())
		ck.add(250 * time.Millisecond)
	}
	return at
}

func wrapFetch(f Fetcher, hook func()) Fetcher {
	return func(ctx context.Context, n string) (Sample, error) {
		hook()
		return f(ctx, n)
	}
}

func TestEvenSpacing(t *testing.T) {
	ctx := context.Background()
	// Two ticks in the same instant send one request.
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001", "50002", "50003")
	s.SetRate(10)
	s.Tick(ctx)
	s.Tick(ctx)
	if fa.count() != 1 {
		t.Fatalf("spacing: %d", fa.count())
	}
	// 10/min: the first request is immediate, then exactly one every 6 s,
	// with no burst at the start.
	s2, ck2 := setup(t, &fakeAPI{}, "50001", "50002", "50003")
	s2.SetRate(10)
	at := reqTimes(s2, ck2, 60*time.Second)
	if len(at) != 11 {
		t.Fatalf("requests in 60 s: %v", at)
	}
	for i, a := range at {
		if a != time.Duration(i)*6*time.Second {
			t.Fatalf("request %d at %v, want %v (all: %v)", i, a, time.Duration(i)*6*time.Second, at)
		}
	}
	// A late tick does not shift the schedule.
	s3, ck3 := setup(t, &fakeAPI{}, "50001")
	s3.SetRate(10)
	s3.Tick(ctx)
	ck3.add(6*time.Second + 400*time.Millisecond)
	s3.Tick(ctx)
	ck3.add(5*time.Second + 600*time.Millisecond) // 12.0 s after the first
	if u := s3.Snapshot(); u.NextAt == nil || !u.NextAt.Equal(newClock().t.Add(12*time.Second)) {
		t.Fatalf("schedule drifted: %v", u.NextAt)
	}
	// The minimum gap holds at the highest rate (25/min = 2.4 s -> 3 s).
	s4, ck4 := setup(t, &fakeAPI{}, "50001", "50002")
	s4.SetRate(25)
	at = reqTimes(s4, ck4, 30*time.Second)
	for i := 1; i < len(at); i++ {
		if at[i]-at[i-1] != 3*time.Second {
			t.Fatalf("gap at 25/min: %v", at)
		}
	}
	// Rate 1/min.
	s5, ck5 := setup(t, &fakeAPI{}, "50001")
	s5.SetRate(1)
	at = reqTimes(s5, ck5, 5*time.Minute)
	if len(at) != 6 || at[1] != time.Minute {
		t.Fatalf("rate 1: %v", at)
	}
	if ClampRate(99) != MaxRate || ClampRate(0) != MinRate {
		t.Fatal("clamp")
	}
	_ = ck
}

func TestCheckingAndEvents(t *testing.T) {
	var s *Service
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001", "50002")
	var ups []Update
	s.OnUpdate = func(u Update) { ups = append(ups, u) }
	inFlight := s.fetch
	var during Update
	s.fetch = func(ctx context.Context, n string) (Sample, error) {
		during = s.Snapshot()
		return inFlight(ctx, n)
	}
	s.Tick(context.Background())
	if during.Checking != "50001" {
		t.Fatalf("checking during request: %q", during.Checking)
	}
	if len(ups) != 2 {
		t.Fatalf("want a start and an end event, got %d", len(ups))
	}
	st, en := ups[0], ups[1]
	if st.Checking != "50001" || len(st.Stats) != 0 || len(st.CheckedAt) != 0 || st.NextAt == nil {
		t.Fatalf("start event: %+v", st)
	}
	if en.Checking != "" || len(en.Stats) != 1 || en.CheckedAt["50001"] != ck.t || en.Checked != 1 || en.Total != 2 {
		t.Fatalf("end event: %+v", en)
	}
	if want := ck.t.Add(6 * time.Second); en.NextAt == nil || !en.NextAt.Equal(want) {
		t.Fatalf("nextAt %v want %v", en.NextAt, want)
	}
	if snap := s.Snapshot(); snap.Checking != "" || snap.Checked != 1 || len(snap.CheckedAt) != 1 {
		t.Fatalf("snapshot: %+v", snap)
	}
	// Unchanged data: the end event still goes out, but without a Stats entry.
	ck.add(6 * time.Second)
	s.Tick(context.Background())
	if len(ups) != 4 || ups[2].Checking != "50002" || ups[3].Checking != "" || ups[3].CheckedAt["50002"] != ck.t {
		t.Fatalf("second request events: %d %+v", len(ups), ups[len(ups)-1])
	}
}

func TestProgressPassRollover(t *testing.T) {
	ctx := context.Background()
	// 50002 is offline: it counts as checked and has a time but no stats.
	fa := &fakeAPI{reply: func(n string) (int, string) {
		if n == "50002" {
			return 200, "null"
		}
		return 200, fixture
	}}
	s, ck := setup(t, fa, "50001", "50002", "50003", "3999999", "1999", "99")
	tick := func() Update {
		s.Tick(ctx)
		u := s.Snapshot()
		ck.add(6 * time.Second)
		return u
	}
	if u := s.Snapshot(); u.Total != 3 || u.Checked != 0 || u.NextAt == nil {
		t.Fatalf("start: %+v", u)
	}
	for i, want := range []int{1, 2, 3} {
		if u := tick(); u.Checked != want || u.Total != 3 {
			t.Fatalf("step %d: checked %d total %d", i, u.Checked, u.Total)
		}
	}
	snap := s.Snapshot()
	if _, ok := snap.Stats["50002"]; ok || snap.CheckedAt["50002"].IsZero() {
		t.Fatalf("offline node: stats=%v checkedAt=%v", snap.Stats, snap.CheckedAt)
	}
	if len(snap.CheckedAt) != 3 {
		t.Fatalf("checkedAt: %v", snap.CheckedAt)
	}
	// Pass complete (30 of 30 style: 3 of 3). The next request starts a new
	// pass; the offline node is in its skip period and stays counted.
	if snap.Checked != 3 {
		t.Fatalf("complete pass: %d", snap.Checked)
	}
	s.Tick(ctx)
	if u := s.Snapshot(); u.Checked != 2 || u.Total != 3 {
		t.Fatalf("after rollover: checked %d total %d", u.Checked, u.Total)
	}
	// Local and invalid nodes never count; no eligible nodes: no nextAt.
	s.Favorites = func() []string { return []string{"1999", "99", "3999999"} }
	if u := s.Snapshot(); u.Total != 0 || u.Checked != 0 || u.NextAt != nil {
		t.Fatalf("nothing eligible: %+v", u)
	}
}

func TestRolloverRestartsCount(t *testing.T) {
	ctx := context.Background()
	s, ck := setup(t, &fakeAPI{}, "50001", "50002")
	for i := 0; i < 2; i++ {
		s.Tick(ctx)
		ck.add(6 * time.Second)
	}
	if u := s.Snapshot(); u.Checked != 2 {
		t.Fatalf("full pass: %d", u.Checked)
	}
	var starts []Update
	s.OnUpdate = func(u Update) {
		if u.Checking != "" {
			starts = append(starts, u)
		}
	}
	s.Tick(ctx)
	if len(starts) != 1 || starts[0].Checked != 0 || starts[0].Checking != "50001" {
		t.Fatalf("new pass starts at 0: %+v", starts)
	}
	if u := s.Snapshot(); u.Checked != 1 {
		t.Fatalf("after first of new pass: %d", u.Checked)
	}
}

func TestBackoff429(t *testing.T) {
	fa := &fakeAPI{reply: func(string) (int, string) { return 429, "slow down" }}
	s, ck := setup(t, fa, "50001")
	ctx := context.Background()
	var ups []Update
	s.OnUpdate = func(u Update) { ups = append(ups, u) }
	s.Tick(ctx)
	if fa.count() != 1 || !s.Snapshot().Backoff {
		t.Fatal("no backoff after 429")
	}
	// Paused for 15 s.
	ck.add(14 * time.Second)
	s.Tick(ctx)
	if fa.count() != 1 {
		t.Fatal("requested during backoff")
	}
	ck.add(2 * time.Second)
	s.Tick(ctx)
	if fa.count() != 2 {
		t.Fatalf("no retry after 15 s: %d", fa.count())
	}
	// Doubles: 30 s, 60 s ... capped at 5 min.
	waits := []time.Duration{30, 60, 120, 240, 300, 300}
	for _, w := range waits {
		before := fa.count()
		ck.add(w*time.Second - time.Second)
		s.Tick(ctx)
		if fa.count() != before {
			t.Fatalf("early retry before %ds", w)
		}
		ck.add(2 * time.Second)
		s.Tick(ctx)
		if fa.count() != before+1 {
			t.Fatalf("no retry after %ds", w)
		}
	}
	announced := false
	for _, u := range ups {
		announced = announced || u.Backoff
	}
	if !announced {
		t.Fatal("backoff not announced")
	}
	// Recovery resets the backoff.
	fa.mu.Lock()
	fa.reply = nil
	fa.mu.Unlock()
	ck.add(6 * time.Minute)
	s.Tick(ctx)
	if s.Snapshot().Backoff || len(s.Snapshot().Stats) != 1 {
		t.Fatal("not recovered")
	}
}

func TestErrorBackoffAndNotFound(t *testing.T) {
	fa := &fakeAPI{reply: func(n string) (int, string) {
		if n == "50001" {
			return 404, "{}"
		}
		return 500, "oops"
	}}
	s, ck := setup(t, fa, "50001", "50002")
	ctx := context.Background()
	s.Tick(ctx) // 404: no backoff
	if s.Snapshot().Backoff {
		t.Fatal("404 caused backoff")
	}
	ck.add(6 * time.Second)
	s.Tick(ctx) // 500: backoff
	if !s.Snapshot().Backoff {
		t.Fatal("500 did not back off")
	}
}

func TestUpdatesChangesOnly(t *testing.T) {
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001")
	var ups []Update
	s.OnUpdate = func(u Update) { ups = append(ups, u) }
	ctx := context.Background()
	s.Tick(ctx)
	if len(ups) != 2 || len(ups[1].Stats) != 1 || ups[1].Updated == nil {
		t.Fatalf("%+v", ups)
	}
	ck.add(6 * time.Second)
	s.Tick(ctx) // identical data: tiny events, the stats map stays empty
	if len(ups) != 4 || len(ups[2].Stats) != 0 || len(ups[3].Stats) != 0 || len(ups[3].CheckedAt) != 1 {
		t.Fatalf("unchanged data: %d %+v", len(ups), ups)
	}
}

func TestFavoritesChangesAreNoticed(t *testing.T) {
	fa := &fakeAPI{}
	favs := []string{"50001"}
	s, ck := setup(t, fa, "50001")
	s.Favorites = func() []string { return favs }
	ctx := context.Background()
	s.Tick(ctx)
	favs = []string{"50002"}
	ck.add(6 * time.Second)
	s.Tick(ctx)
	snap := s.Snapshot()
	if _, ok := snap.Stats["50001"]; ok || len(snap.Stats) != 1 {
		t.Fatalf("%+v", snap.Stats)
	}
}

func TestDemoFetcher(t *testing.T) {
	ck := newClock()
	f := DemoFetcher(ck.now)
	smp, err := f(context.Background(), "50001")
	if err != nil || !smp.HaveCounters || smp.Uptime <= 0 {
		t.Fatalf("%+v %v", smp, err)
	}
}

// run ticks every 10 s until done() or max steps; it returns the steps used.
func run(s *Service, ck *clock, max int, done func() bool) int {
	for i := 0; i < max; i++ {
		if done() {
			return i
		}
		ck.add(10 * time.Second)
		s.Tick(context.Background())
	}
	return max
}

func TestAdaptiveRateDecreaseAndIncrease(t *testing.T) {
	fa := &fakeAPI{reply: func(string) (int, string) { return 429, "no" }}
	s, ck := setup(t, fa, "50001")
	s.SetRate(20)
	ctx := context.Background()
	if e := s.Snapshot().EffRate; e != 20 {
		t.Fatalf("start %d", e)
	}
	// Each 429 halves: 20 -> 10 -> 5 -> 3 (floor) -> 3.
	for _, want := range []int{10, 5, 3, 3} {
		s.Tick(ctx)
		if e := s.Snapshot().EffRate; e != want {
			t.Fatalf("after 429: eff %d want %d", e, want)
		}
		ck.add(6 * time.Minute)
	}
	if s.Snapshot().Rate != 20 {
		t.Fatal("configured rate changed")
	}
	// Recovery: +1/min after every 20 successful requests, up to the configured rate.
	fa.mu.Lock()
	fa.reply = nil
	fa.mu.Unlock()
	base := fa.count()
	run(s, ck, 5000, func() bool { return s.Snapshot().EffRate == 4 })
	if n := fa.count() - base; n != 20 {
		t.Fatalf("successes for first increase: %d want 20", n)
	}
	run(s, ck, 20000, func() bool { return s.Snapshot().EffRate == 20 })
	if e := s.Snapshot().EffRate; e != 20 {
		t.Fatalf("never recovered: %d", e)
	}
	before := fa.count()
	run(s, ck, 100, func() bool { return fa.count() >= before+60 })
	if e := s.Snapshot().EffRate; e != 20 {
		t.Fatalf("rose above the configured rate: %d", e)
	}
}

func TestAdaptiveFloorAndStreakReset(t *testing.T) {
	fa := &fakeAPI{reply: func(string) (int, string) { return 429, "no" }}
	s, ck := setup(t, fa, "50001")
	s.SetRate(2) // below the floor: stays at 2
	s.Tick(context.Background())
	if e := s.Snapshot().EffRate; e != 2 {
		t.Fatalf("rate 2: eff %d", e)
	}
	// A 429 in the middle of a run restarts the count of successes.
	s2, ck2 := setup(t, &fakeAPI{}, "50001")
	s2.SetRate(10)
	s2.mu.Lock()
	s2.effRate = 5
	s2.mu.Unlock()
	fa2 := s2.fetch
	var calls int
	s2.fetch = func(ctx context.Context, n string) (Sample, error) {
		calls++
		if calls == 15 {
			return Sample{}, &HTTPError{Status: 429}
		}
		return fa2(ctx, n)
	}
	run(s2, ck2, 5000, func() bool { return calls >= 14 })
	run(s2, ck2, 5000, func() bool { return calls >= 15 })
	if e := s2.Snapshot().EffRate; e != 3 { // 5/2 = 2 -> floor 3
		t.Fatalf("eff %d", e)
	}
	run(s2, ck2, 5000, func() bool { return calls >= 15+19 })
	if e := s2.Snapshot().EffRate; e != 3 {
		t.Fatalf("raised after 19 successes: %d", e)
	}
	run(s2, ck2, 5000, func() bool { return calls >= 15+20 })
	if e := s2.Snapshot().EffRate; e != 4 {
		t.Fatalf("not raised after 20: %d", e)
	}
	_ = ck
}

func TestRateLimitKeepsValues(t *testing.T) {
	limited := false
	fa := &fakeAPI{reply: func(string) (int, string) {
		if limited {
			return 429, "no"
		}
		return 200, fixture
	}}
	s, ck := setup(t, fa, "50001")
	ctx := context.Background()
	s.Tick(ctx)
	before := s.Snapshot()
	if len(before.Stats) != 1 || before.Updated == nil {
		t.Fatalf("%+v", before)
	}
	limited = true
	ck.add(10 * time.Second)
	s.Tick(ctx)
	after := s.Snapshot()
	if !after.Backoff || after.RetryAt == nil || !after.RetryAt.After(ck.now()) {
		t.Fatalf("no backoff info: %+v", after)
	}
	if len(after.Stats) != 1 || !after.Stats["50001"].LastSeen.Equal(before.Stats["50001"].LastSeen) || !after.Updated.Equal(*before.Updated) {
		t.Fatalf("429 changed the values: %+v", after)
	}
}

func TestOrderNeverFetchedThenOldest(t *testing.T) {
	limit := map[string]bool{"50002": true}
	fa := &fakeAPI{reply: func(n string) (int, string) {
		if limit[n] {
			return 429, "no"
		}
		return 200, fixture
	}}
	favs := []string{"50001", "50002", "50003", "50004"}
	s, ck := setup(t, fa, "x")
	s.Favorites = func() []string { return favs }
	ctx := context.Background()
	step := func() { ck.add(10 * time.Minute); s.Tick(ctx) }
	s.Tick(ctx) // 50001
	step()      // 50002: 429, not recorded
	delete(limit, "50002")
	step() // 50002 again (never answered, still first in line)
	step() // 50003
	step() // 50004
	want := "50001,50002,50002,50003,50004"
	if got := strings.Join(fa.hits, ","); got != want {
		t.Fatalf("hits %s want %s", got, want)
	}
	// Oldest answered first, whatever the favorites order.
	favs = []string{"50004", "50003", "50002", "50001"}
	step()
	step()
	if got := strings.Join(fa.hits[5:], ","); got != "50001,50002" {
		t.Fatalf("oldest-first: %s", got)
	}
	// A new favorite jumps the queue.
	favs = append(favs, "50005")
	step()
	if fa.hits[len(fa.hits)-1] != "50005" {
		t.Fatalf("new favorite not first: %v", fa.hits)
	}
}

func TestRequestLog(t *testing.T) {
	for _, on := range []bool{true, false} {
		var buf bytes.Buffer
		fa := &fakeAPI{reply: func(n string) (int, string) {
			if n == "50002" {
				return 429, "secret body"
			}
			return 200, fixture
		}}
		ts := httptest.NewServer(fa)
		ck := newClock()
		s := New(Options{BaseURL: ts.URL, Logger: log.New(&buf, "", 0), Now: ck.now, LogRequests: on})
		s.Favorites = func() []string { return []string{"50001", "50002"} }
		s.SetEnabled(true)
		s.SetViewers(1)
		s.Tick(context.Background())
		ck.add(10 * time.Second)
		s.Tick(context.Background())
		ts.Close()
		out := buf.String()
		has := strings.Contains(out, "favstats: GET node=50001 status=200 took=0ms rate=20/min gap=3s retry-after=-")
		if on {
			if !has || !strings.Contains(out, "favstats: GET node=50002 status=429 took=0ms rate=20/min") || !strings.Contains(out, "retry-after=1s") ||
				!strings.Contains(out, "favstats: rate lowered to 10/min after 429") {
				t.Fatalf("log on: %q", out)
			}
			if strings.Contains(out, "secret body") || strings.Contains(out, "N0CALL") {
				t.Fatalf("response data logged: %q", out)
			}
		} else if strings.Contains(out, "GET node=") || strings.Contains(out, "rate lowered") {
			t.Fatalf("log off but logged: %q", out)
		}
	}
}

func TestDecodeOddTypes(t *testing.T) {
	huge := `[` + strings.Repeat(`{"callsign":"N0CALL","x":[1,2,{"y":null}]},`, 20000) + `{}]`
	cases := []struct {
		name, body string
		want       Sample
	}{
		{"strings", `{"stats":{"data":{"apprptuptime":"100","totaltxtime":"20","totalkeyups":"3","keyed":"1","links":["1","2"]}},"node":{"Status":"Active","access_webtransceiver":"1"}}`,
			Sample{Uptime: 100, TxTime: 20, KeyUps: 3, HaveCounters: true, Keyed: true, Links: 2, Active: true, WebTx: true}},
		{"numbers", `{"stats":{"data":{"apprptuptime":100,"totaltxtime":20.5,"totalkeyups":3,"keyed":0,"links":[50002,50003,50004]}},"node":{"Status":"Active","access_webtransceiver":1}}`,
			Sample{Uptime: 100, TxTime: 20.5, KeyUps: 3, HaveCounters: true, Links: 3, Active: true, WebTx: true}},
		{"keyed zero string", `{"stats":{"data":{"apprptuptime":1,"totaltxtime":1,"totalkeyups":1,"keyed":"0"}}}`, Sample{Uptime: 1, TxTime: 1, KeyUps: 1, HaveCounters: true}},
		{"keyed word", `{"stats":{"data":{"apprptuptime":1,"totaltxtime":1,"totalkeyups":1,"keyed":"true"}}}`, Sample{Uptime: 1, TxTime: 1, KeyUps: 1, HaveCounters: true, Keyed: true}},
		{"bools and nulls", `{"stats":{"data":{"apprptuptime":null,"totaltxtime":true,"totalkeyups":5,"keyed":true,"links":null}},"node":{"Status":null,"access_webtransceiver":true}}`,
			Sample{KeyUps: 5, Keyed: true, WebTx: true}},
		{"wrong types", `{"stats":{"data":{"apprptuptime":{"a":1},"totaltxtime":[1],"totalkeyups":5,"keyed":[],"links":{"a":1}}},"node":{"Status":["Active"],"access_webtransceiver":{}}}`,
			Sample{KeyUps: 5}},
		{"node null", `{"stats":{"data":{"links":[]}},"node":null}`, Sample{}},
		{"node array", `{"node":[{"Status":"Active"}],"stats":{"data":{"links":[1]}}}`, Sample{Links: 1}},
		{"node missing", `{"stats":{"data":{"apprptuptime":"9"}}}`, Sample{Uptime: 9}},
		{"node number", `{"stats":{"data":{"apprptuptime":"9"}},"node":5}`, Sample{Uptime: 9}},
		{"huge linkedNodes", `{"stats":{"data":{"apprptuptime":"9","totaltxtime":"1","totalkeyups":"1","linkedNodes":` + huge + `}},"linkedNodes":` + huge + `,"node":{"Status":"active"}}`,
			Sample{Uptime: 9, TxTime: 1, KeyUps: 1, HaveCounters: true, Active: true}},
	}
	for _, c := range cases {
		got, err := Decode(strings.NewReader(c.body))
		if err != nil || got != c.want {
			t.Errorf("%s: %+v %v want %+v", c.name, got, err, c.want)
		}
	}
}

func TestDecodeNoData(t *testing.T) {
	for name, body := range map[string]string{
		"empty": "", "blank": " \n", "null": "null", "object": "{}", "stats null": `{"stats":null}`,
		"stats no data": `{"stats":{"node":1}}`, "data null": `{"stats":{"data":null}}`,
		"error": `{"error":"not found"}`, "message": `{"message":"offline"}`, "array": "[]",
		"html": "<!doctype html><html></html>", "html ws": "\n  <html>",
	} {
		if _, err := Decode(strings.NewReader(body)); !errors.Is(err, errNoData) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, body := range map[string]string{"garbage": "garbage", "truncated": `{"stats":{"data":{"apprptuptime":"1"`, "bad": `{"stats":}`} {
		_, err := Decode(strings.NewReader(body))
		if !isDecodeErr(err) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCapReader(t *testing.T) {
	r := &capReader{r: strings.NewReader("abcd"), left: 4}
	if b, err := io.ReadAll(r); err != nil || string(b) != "abcd" {
		t.Fatal(string(b), err)
	}
	r = &capReader{r: strings.NewReader("abcde"), left: 4}
	if _, err := io.ReadAll(r); !errors.Is(err, errTooLarge) {
		t.Fatal(err)
	}
}

// skipLog sets up two favorites where the first answers per reply, with a log buffer.
func skipSetup(t *testing.T, reply func(string) (int, string)) (*Service, *clock, *fakeAPI, *bytes.Buffer) {
	var buf bytes.Buffer
	fa := &fakeAPI{reply: reply}
	ts := httptest.NewServer(fa)
	t.Cleanup(ts.Close)
	ck := newClock()
	s := New(Options{BaseURL: ts.URL, Logger: log.New(&buf, "", 0), Now: ck.now, LogRequests: true})
	s.Favorites = func() []string { return []string{"50001", "50002"} }
	s.SetEnabled(true)
	s.SetViewers(1)
	return s, ck, fa, &buf
}

func TestTooLargeSkipsNodeOnly(t *testing.T) {
	big := `{"stats":{"data":{"apprptuptime":"1","linkedNodes":"` + strings.Repeat("x", maxBody) + `"}}}`
	s, ck, fa, buf := skipSetup(t, func(n string) (int, string) {
		if n == "50001" {
			return 200, big
		}
		return 200, fixture
	})
	ctx := context.Background()
	s.Tick(ctx)
	if s.Snapshot().Backoff || len(s.Snapshot().Stats) != 0 {
		t.Fatal("global backoff or sample after too-large")
	}
	out := buf.String()
	if !strings.Contains(out, "favstats: node 50001: response too large (>4 MB), skipping for 10m0s") || !strings.Contains(out, "status=too-large") {
		t.Fatalf("log: %q", out)
	}
	// The next request goes right away, to the other node; 50001 stays out for 10 minutes.
	for i := 0; i < 6; i++ {
		ck.add(10 * time.Second)
		s.Tick(ctx)
	}
	if got := strings.Join(fa.hits, ","); got != "50001,50002,50002,50002,50002,50002,50002" {
		t.Fatalf("hits %s", got)
	}
	ck.add(10 * time.Minute)
	for i := 0; i < 3; i++ {
		s.Tick(ctx)
		ck.add(10 * time.Second)
	}
	if strings.Count(strings.Join(fa.hits, ","), "50001") != 2 {
		t.Fatalf("50001 not retried: %v", fa.hits)
	}
}

func TestDecodeErrorSkipsNodeOnly(t *testing.T) {
	s, ck, fa, buf := skipSetup(t, func(n string) (int, string) {
		if n == "50001" {
			return 200, `{"stats":}`
		}
		return 200, fixture
	})
	ctx := context.Background()
	s.Tick(ctx)
	s.mu.Lock()
	s.okStreak = 7
	s.mu.Unlock()
	if s.Snapshot().Backoff {
		t.Fatal("global backoff after decode error")
	}
	if out := buf.String(); !strings.Contains(out, "favstats: node 50001: invalid response (invalid character '}' looking for beginning of value), skipping for 10m0s") ||
		!strings.Contains(out, "status=decode-error") {
		t.Fatalf("log: %q", out)
	}
	ck.add(10 * time.Second)
	s.Tick(ctx)
	s.mu.Lock()
	streak := s.okStreak
	s.mu.Unlock()
	if streak != 8 || len(s.Snapshot().Stats) != 1 || fa.hits[1] != "50002" {
		t.Fatalf("streak %d hits %v", streak, fa.hits)
	}
}

func TestNoDataIsQuiet(t *testing.T) {
	for name, body := range map[string]string{"empty": "", "null": "null", "obj": "{}", "err": `{"error":"x"}`, "html": "<html>"} {
		s, ck, fa, buf := skipSetup(t, func(n string) (int, string) {
			if n == "50001" {
				return 200, body
			}
			return 200, fixture
		})
		ctx := context.Background()
		s.Tick(ctx) // good first sample would exist; here node 50001 is the offline one
		if s.Snapshot().Backoff || len(s.Snapshot().Stats) != 0 {
			t.Fatalf("%s: backoff or sample", name)
		}
		out := buf.String()
		if !strings.Contains(out, "node=50001 status=200-nodata") || strings.Contains(out, "skipping") || strings.Contains(out, "failed") || strings.Contains(out, "invalid") {
			t.Fatalf("%s log: %q", name, out)
		}
		ck.add(10 * time.Second)
		s.Tick(ctx)
		if fa.hits[1] != "50002" {
			t.Fatalf("%s: hits %v", name, fa.hits)
		}
	}
}

func TestNoDataClearsValues(t *testing.T) {
	offline := false
	s, ck, _, _ := skipSetup(t, func(n string) (int, string) {
		if n == "50001" && offline {
			return 200, "{}"
		}
		return 200, fixture
	})
	ctx := context.Background()
	s.Tick(ctx)
	if _, ok := s.Snapshot().Stats["50001"]; !ok {
		t.Fatal("no values")
	}
	offline = true
	for i := 0; i < 4; i++ { // 50002, then 50001 again once it is oldest
		ck.add(10 * time.Second)
		s.Tick(ctx)
	}
	if _, ok := s.Snapshot().Stats["50001"]; ok {
		t.Fatal("offline node keeps values")
	}
}

func TestClientErrorSkipsNode(t *testing.T) {
	s, _, _, buf := skipSetup(t, func(n string) (int, string) { return 403, "secret body" })
	s.Tick(context.Background())
	if s.Snapshot().Backoff || !strings.Contains(buf.String(), "favstats: node 50001: HTTP 403, skipping for 10m0s") || strings.Contains(buf.String(), "secret") {
		t.Fatalf("%q", buf.String())
	}
}

func TestTimeoutBacksOffGlobally(t *testing.T) {
	var buf bytes.Buffer
	s := New(Options{Logger: log.New(&buf, "", 0), Now: newClock().now, Fetch: func(ctx context.Context, n string) (Sample, error) {
		return Sample{}, &url.Error{Op: "Get", URL: "http://x/N0CALL", Err: context.DeadlineExceeded}
	}})
	s.Favorites = func() []string { return []string{"50001"} }
	s.SetEnabled(true)
	s.SetViewers(1)
	s.Tick(context.Background())
	if !s.Snapshot().Backoff || !strings.Contains(buf.String(), "favstats: request failed (timeout), pausing 15s") || strings.Contains(buf.String(), "N0CALL") {
		t.Fatalf("%q", buf.String())
	}
	if errKind(fmt.Errorf("read: %w", context.DeadlineExceeded)) != "timeout" {
		t.Fatal("body read timeout")
	}
}

func TestServerErrorBacksOffGlobally(t *testing.T) {
	s, _, _, buf := skipSetup(t, func(string) (int, string) { return 503, "" })
	s.Tick(context.Background())
	if !s.Snapshot().Backoff || !strings.Contains(buf.String(), "HTTP 503, pausing 15s") {
		t.Fatalf("%q", buf.String())
	}
}

func TestCleanReason(t *testing.T) {
	got := cleanReason("a\nb" + strings.Repeat("é", 300))
	if strings.Contains(got, "\n") || len([]rune(got)) != 123 || !strings.HasSuffix(got, "...") {
		t.Fatalf("%q", got)
	}
}

func TestRegistrationActiveNeedsLiveData(t *testing.T) {
	// Offline node: stats null, registration record says Active.
	off := `{"stats":null,"node":{"Status":"Active","access_webtransceiver":"0","callsign":"N0CALL","ipaddr":"192.0.2.1","server":{"Location":"x"}},"keyups":[],"time":1.10}`
	if _, err := Decode(strings.NewReader(off)); !errors.Is(err, errNoData) {
		t.Fatal(err)
	}
	// Top-level time (float) and keyups (array) are ignored.
	smp, err := Decode(strings.NewReader(`{"stats":{"data":{"apprptuptime":"9","time":"1700000000"}},"node":{"Status":"Active"},"keyups":[{"a":1}],"time":0.5}`))
	if err != nil || smp.Uptime != 9 || smp.DataTime != 1700000000 || !smp.Active {
		t.Fatalf("%+v %v", smp, err)
	}
	ck := newClock()
	s := New(Options{Logger: quiet(), Now: ck.now})
	now := float64(ck.now().Unix())
	for _, c := range []struct {
		dt   float64
		want bool
	}{{0, true}, {now - 60, true}, {now - 3600, false}, {now + 3600, false}} {
		a := s.compute("50001", Sample{Active: true, WebTx: true, DataTime: c.dt}, ck.now())
		if a.Active != c.want || a.WebTx != c.want {
			t.Errorf("time %v: %+v", c.dt, a)
		}
	}
}

func TestClearedNodeEmitsRemoval(t *testing.T) {
	offline := false
	s, ck, _, _ := skipSetup(t, func(n string) (int, string) {
		if n == "50001" && offline {
			return 200, "{}"
		}
		return 200, fixture
	})
	var ups []Update
	s.OnUpdate = func(u Update) { ups = append(ups, u) }
	ctx := context.Background()
	s.Tick(ctx)
	if len(ups) != 2 || len(ups[1].Removed) != 0 || ups[1].Removed == nil {
		t.Fatalf("first update: %+v", ups)
	}
	offline = true
	var got *Update
	for i := 0; i < 4 && got == nil; i++ {
		ck.add(10 * time.Second)
		s.Tick(ctx)
		for j := range ups {
			if len(ups[j].Removed) > 0 {
				got = &ups[j]
			}
		}
	}
	if got == nil {
		t.Fatal("clearing a node emitted no removal")
	}
	if len(got.Removed) != 1 || got.Removed[0] != "50001" {
		t.Fatalf("removed = %v", got.Removed)
	}
	if _, ok := got.Stats["50001"]; ok {
		t.Fatal("removed node also in stats")
	}
	if b, _ := json.Marshal(s.Snapshot()); !strings.Contains(string(b), `"removed":[]`) {
		t.Fatalf("snapshot must carry an empty removed list: %s", b)
	}
}

func TestDefaultRate(t *testing.T) {
	ck := newClock()
	s := New(Options{Logger: quiet(), Now: ck.now, Fetch: func(context.Context, string) (Sample, error) { return Sample{Links: 1}, nil }})
	s.Favorites = func() []string { return []string{"50001", "50002"} }
	s.SetEnabled(true)
	s.SetViewers(1)
	if u := s.Snapshot(); u.Rate != 20 || u.EffRate != 20 {
		t.Fatalf("default rate: %+v", u)
	}
	at := reqTimes(s, ck, 15*time.Second)
	if len(at) != 6 {
		t.Fatalf("requests: %v", at)
	}
	for i, a := range at {
		if a != time.Duration(i)*3*time.Second {
			t.Fatalf("request %d at %v: %v", i, a, at)
		}
	}
}

func TestBackoffReason(t *testing.T) {
	for _, c := range []struct {
		err  error
		want string
	}{
		{&HTTPError{Status: 429}, "rate-limit"},
		{&HTTPError{Status: 503}, "service-error"},
		{context.DeadlineExceeded, "network-error"},
	} {
		s, ck := setup(t, &fakeAPI{}, "50001")
		s.fetch = func(context.Context, string) (Sample, error) { return Sample{}, c.err }
		var ups []Update
		s.OnUpdate = func(u Update) { ups = append(ups, u) }
		s.Tick(context.Background())
		if u := s.Snapshot(); !u.Backoff || u.BackoffReason != c.want {
			t.Fatalf("%v: %+v", c.err, u)
		}
		if last := ups[len(ups)-1]; last.BackoffReason != c.want {
			t.Fatalf("event: %+v", last)
		}
		// Recovery clears the reason.
		s.fetch = func(context.Context, string) (Sample, error) { return Sample{Links: 1}, nil }
		ck.add(6 * time.Minute)
		s.Tick(context.Background())
		if u := s.Snapshot(); u.Backoff || u.BackoffReason != "" {
			t.Fatalf("after recovery: %+v", u)
		}
	}
	// Offline nodes never cause a backoff.
	s, _ := setup(t, &fakeAPI{reply: func(string) (int, string) { return 200, "null" }}, "50001")
	s.Tick(context.Background())
	if u := s.Snapshot(); u.Backoff || u.BackoffReason != "" {
		t.Fatalf("offline node: %+v", u)
	}
}

func TestNextAnnouncedIsRequested(t *testing.T) {
	ctx := context.Background()
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001", "50002", "50003", "3500000", "1999")
	favs := []string{"50001", "50002", "50003", "3500000", "1999"}
	s.Favorites = func() []string { return favs }
	var ups []Update
	s.OnUpdate = func(u Update) { ups = append(ups, u) }
	var requested []string
	inner := s.fetch
	s.fetch = func(c context.Context, n string) (Sample, error) {
		requested = append(requested, n)
		return inner(c, n)
	}
	// Locals and EchoLink are never announced; never-fetched come first in order.
	if got := s.Snapshot().Next; got != "50001" {
		t.Fatalf("first next %q", got)
	}
	for i := 0; i < 7; i++ {
		announced := s.Snapshot().Next
		before := len(requested)
		s.Tick(ctx)
		if len(requested) != before+1 || requested[before] != announced {
			t.Fatalf("step %d: announced %q, requested %v", i, announced, requested)
		}
		// The start event already points past the node being requested, the
		// end event says the same thing.
		st, en := ups[len(ups)-2], ups[len(ups)-1]
		if st.Checking != announced || st.Next == announced || en.Next != s.Snapshot().Next {
			t.Fatalf("step %d: start %+v end %+v", i, st, en)
		}
		ck.add(6 * time.Second)
	}
	if want := []string{"50001", "50002", "50003", "50001", "50002", "50003", "50001"}; strings.Join(requested, ",") != strings.Join(want, ",") {
		t.Fatalf("requested %v", requested)
	}
}

func TestNextEmptyStates(t *testing.T) {
	ctx := context.Background()
	fa := &fakeAPI{}
	s, ck := setup(t, fa, "50001", "50002")
	if s.Snapshot().Next == "" {
		t.Fatal("next while scanning")
	}
	s.SetViewers(0)
	if got := s.Snapshot().Next; got != "" {
		t.Fatalf("no viewers: %q", got)
	}
	s.SetViewers(1)
	s.SetEnabled(false)
	if got := s.Snapshot().Next; got != "" {
		t.Fatalf("off: %q", got)
	}
	s.SetEnabled(true)
	s.Favorites = func() []string { return []string{"1999", "abc"} }
	if got := s.Snapshot().Next; got != "" {
		t.Fatalf("nothing eligible: %q", got)
	}
	// Both nodes in their skip period: nothing to announce.
	s.Favorites = func() []string { return []string{"50001"} }
	fa.reply = func(string) (int, string) { return 404, "{}" }
	s.Tick(ctx)
	ck.add(6 * time.Second)
	if got := s.Snapshot().Next; got != "" {
		t.Fatalf("all skipped: %q", got)
	}
	_ = ck
}

func TestNextFollowsFavoritesChange(t *testing.T) {
	ctx := context.Background()
	s, ck := setup(t, &fakeAPI{}, "50001", "50002")
	favs := []string{"50001", "50002"}
	s.Favorites = func() []string { return favs }
	s.Tick(ctx) // 50001 asked
	if got := s.Snapshot().Next; got != "50002" {
		t.Fatalf("next %q", got)
	}
	var ups []Update
	s.OnUpdate = func(u Update) { ups = append(ups, u) }
	s.Refresh() // nothing changed: no event
	if len(ups) != 0 {
		t.Fatalf("spurious event %+v", ups)
	}
	favs = []string{"50001", "50009", "50002"} // a new, never-asked favorite
	s.Refresh()
	if len(ups) != 1 || ups[0].Next != "50009" {
		t.Fatalf("refresh event %+v", ups)
	}
	ck.add(6 * time.Second)
	before := len(ups)
	s.Tick(ctx)
	if ups[before].Checking != "50009" {
		t.Fatalf("requested %q, announced 50009", ups[before].Checking)
	}
}
