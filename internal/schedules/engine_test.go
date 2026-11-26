package schedules

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

var ny = mustLoc("America/New_York")

func mustLoc(n string) *time.Location {
	l, err := time.LoadLocation(n)
	if err != nil {
		panic(err)
	}
	return l
}

// tm builds a time in New York: tm(2026, 11, 17, 19, 0, 5).
func tm(y int, mo time.Month, d, h, mi, s int) time.Time { return time.Date(y, mo, d, h, mi, s, 0, ny) }

// 2026-11-17 is a Tuesday.
func tue(h, mi, s int) time.Time { return tm(2026, 11, 17, h, mi, s) }

type fakeStore struct {
	mu       sync.Mutex
	list     []Schedule
	settings map[string]string
	results  map[int64]string
}

func newFakeStore(l ...Schedule) *fakeStore {
	return &fakeStore{list: l, settings: map[string]string{}, results: map[int64]string{}}
}

func (f *fakeStore) ListSchedules() ([]Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Schedule(nil), f.list...), nil
}
func (f *fakeStore) SetScheduleResult(id int64, at time.Time, r string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.results[id] = r
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].LastResult = r
			f.list[i].LastRun = &at
		}
	}
	return nil
}
func (f *fakeStore) SetScheduleEnabled(id int64, on bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == id {
			f.list[i].Enabled = on
		}
	}
	return nil
}
func (f *fakeStore) GetSetting(k string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.settings[k]
	if !ok {
		return "", errors.New("not found")
	}
	return v, nil
}
func (f *fakeStore) SetSetting(k, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings[k] = v
	return nil
}
func (f *fakeStore) get(id int64) Schedule {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.list {
		if s.ID == id {
			return s
		}
	}
	return Schedule{}
}
func (f *fakeStore) set(s Schedule) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.list {
		if f.list[i].ID == s.ID {
			f.list[i] = s
			return
		}
	}
	f.list = append(f.list, s)
}

type fakeLinker struct {
	mu      sync.Mutex
	calls   []string
	links   map[string]string // "node>target" -> mode
	err     error
	unknown bool
}

func newFakeLinker() *fakeLinker { return &fakeLinker{links: map[string]string{}} }

func (l *fakeLinker) Connect(_ context.Context, m, target string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("connect %s>%s", m, target))
	if l.err != nil {
		return l.err
	}
	l.links[m+">"+target] = "linked"
	return nil
}
func (l *fakeLinker) Disconnect(_ context.Context, m, target string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, fmt.Sprintf("disconnect %s>%s", m, target))
	if l.err != nil {
		return l.err
	}
	delete(l.links, m+">"+target)
	return nil
}
func (l *fakeLinker) Linked(m, target string) (bool, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.unknown {
		return false, false
	}
	_, ok := l.links[m+">"+target]
	return ok, true
}

func (l *fakeLinker) take() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	c := l.calls
	l.calls = nil
	return c
}
func (l *fakeLinker) setErr(err error) {
	l.mu.Lock()
	l.err = err
	l.mu.Unlock()
}

type harness struct {
	t    *testing.T
	st   *fakeStore
	lk   *fakeLinker
	e    *Engine
	now  time.Time
	mu   sync.Mutex
	chgs int
}

func newHarness(t *testing.T, start time.Time, list ...Schedule) *harness {
	t.Helper()
	h := &harness{t: t, st: newFakeStore(list...), lk: newFakeLinker(), now: start}
	h.e = h.engine()
	return h
}

func (h *harness) engine() *Engine {
	e := h.newEngine()
	e.mu.Lock()
	e.init() // the engine "starts" at the harness clock
	e.mu.Unlock()
	return e
}

func (h *harness) newEngine() *Engine {
	return &Engine{Store: h.st, Linker: h.lk, Loc: ny, Zone: "America/New_York",
		Now:      func() time.Time { h.mu.Lock(); defer h.mu.Unlock(); return h.now },
		OnChange: func() { h.mu.Lock(); h.chgs++; h.mu.Unlock() }}
}

// at moves the clock and runs one tick.
func (h *harness) at(now time.Time) []string {
	h.t.Helper()
	h.mu.Lock()
	h.now = now
	h.mu.Unlock()
	h.e.RunOnce(context.Background())
	return h.lk.take()
}

func (h *harness) clock(now time.Time) {
	h.mu.Lock()
	h.now = now
	h.mu.Unlock()
}

func win(id int64, days []int, start, end string) Schedule {
	return Schedule{ID: id, Enabled: true, Mode: "allstar", Target: "41234", Action: ActionConnect,
		Days: days, Start: start, End: end}
}

func same(t *testing.T, got []string, want ...string) {
	t.Helper()
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("commands:\n got  %q\n want %q", got, want)
	}
}

func TestWindowConnectAndDisconnect(t *testing.T) {
	s := win(1, []int{2}, "19:00", "20:30")
	h := newHarness(t, tue(18, 59, 50), s)
	same(t, h.at(tue(18, 59, 55)))
	same(t, h.at(tue(19, 0, 5)), "connect allstar>41234")
	if r := h.st.get(1).LastResult; r != "ok" {
		t.Fatalf("result %q", r)
	}
	if h.st.get(1).LastRun == nil {
		t.Fatal("lastRun not set")
	}
	snap := h.e.Snapshot()
	if len(snap.Active) != 1 || snap.Active[0].ID != 1 || !snap.Active[0].End.Equal(tue(20, 30, 0)) {
		t.Fatalf("active: %+v", snap.Active)
	}
	same(t, h.at(tue(19, 30, 0)))
	same(t, h.at(tue(20, 30, 5)), "disconnect allstar>41234")
	if len(h.e.Snapshot().Active) != 0 {
		t.Fatal("still active")
	}
	same(t, h.at(tue(20, 31, 0)))
	// next week again
	same(t, h.at(tm(2026, 11, 24, 19, 0, 1)), "connect allstar>41234")
}

func TestSkipWhenAlreadyLinkedStillDisconnects(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.lk.links["allstar>41234"] = "linked"
	same(t, h.at(tue(19, 0, 5)))
	if r := h.st.get(1).LastResult; r != "skipped: already connected" {
		t.Fatalf("result %q", r)
	}
	same(t, h.at(tue(20, 0, 5)), "disconnect allstar>41234")
}

func TestWindowEndDisconnectsWhenLinkAbsent(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	same(t, h.at(tue(19, 0, 5)), "connect allstar>41234")
	delete(h.lk.links, "allstar>41234") // dropped by itself
	same(t, h.at(tue(20, 0, 5)))
	if r := h.st.get(1).LastResult; r != "skipped: not connected" {
		t.Fatalf("result %q", r)
	}
}

func TestDisconnectSchedule(t *testing.T) {
	d := Schedule{ID: 2, Enabled: true, Mode: "allstar", Target: "41234", Action: ActionDisconnect, Days: []int{2}, Start: "07:00"}
	h := newHarness(t, tue(6, 59, 50), d)
	h.lk.links["allstar>41234"] = "linked"
	same(t, h.at(tue(7, 0, 5)), "disconnect allstar>41234")
	if r := h.st.get(2).LastResult; r != "ok" {
		t.Fatal(r)
	}
	// not linked: skipped
	h = newHarness(t, tue(6, 59, 50), d)
	same(t, h.at(tue(7, 0, 5)))
	if r := h.st.get(2).LastResult; r != "skipped: not connected" {
		t.Fatal(r)
	}
}

func TestConnectWithoutEnd(t *testing.T) {
	c := win(3, []int{2}, "19:00", "")
	h := newHarness(t, tue(18, 59, 50), c)
	same(t, h.at(tue(19, 0, 5)), "connect allstar>41234")
	same(t, h.at(tue(19, 1, 5)))
	same(t, h.at(tue(23, 0, 5)))
}

func TestSingleEventsNotReplayedAtStart(t *testing.T) {
	h := newHarness(t, tue(19, 0, 30), win(3, []int{2}, "19:00", ""))
	same(t, h.at(tue(19, 0, 35)))
	same(t, h.at(tue(19, 1, 35)))
}

func TestRetryEveryMinuteForWindow(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "19:05"))
	h.lk.setErr(errors.New("AMI down"))
	same(t, h.at(tue(19, 0, 5)), "connect allstar>41234")
	if r := h.st.get(1).LastResult; r != "error: AMI down" {
		t.Fatalf("result %q", r)
	}
	same(t, h.at(tue(19, 0, 35))) // not due yet
	same(t, h.at(tue(19, 1, 6)), "connect allstar>41234")
	same(t, h.at(tue(19, 2, 7)), "connect allstar>41234")
	h.lk.setErr(nil)
	same(t, h.at(tue(19, 3, 8)), "connect allstar>41234")
	if r := h.st.get(1).LastResult; r != "ok" {
		t.Fatalf("result %q", r)
	}
	same(t, h.at(tue(19, 4, 9)))
	same(t, h.at(tue(19, 5, 5)), "disconnect allstar>41234")
}

func TestWindowRetriesStopAtEnd(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "19:03"))
	h.lk.setErr(errors.New("boom"))
	for _, at := range []time.Time{tue(19, 0, 5), tue(19, 1, 6), tue(19, 2, 7)} {
		if c := h.at(at); len(c) != 1 {
			t.Fatalf("at %v: %v", at, c)
		}
	}
	same(t, h.at(tue(19, 3, 8)))
	same(t, h.at(tue(19, 10, 0)))
}

func TestSingleRetryForTenMinutes(t *testing.T) {
	c := win(3, []int{2}, "19:00", "")
	h := newHarness(t, tue(18, 59, 50), c)
	h.lk.setErr(errors.New("down"))
	n := 0
	for m := 0; m <= 14; m++ {
		n += len(h.at(tue(19, m, 6)))
	}
	if n != 10 { // 19:00 ... 19:09
		t.Fatalf("attempts %d", n)
	}
	h.lk.setErr(nil)
	same(t, h.at(tue(19, 20, 0)))
}

func TestNodeUnreachableIsError(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", ""))
	h.lk.unknown = true
	same(t, h.at(tue(19, 0, 5)))
	if r := h.st.get(1).LastResult; !strings.HasPrefix(r, "error:") {
		t.Fatalf("result %q", r)
	}
	h.lk.unknown = false
	same(t, h.at(tue(19, 1, 6)), "connect allstar>41234")
}

func TestCatchUpOnStart(t *testing.T) {
	h := newHarness(t, tue(19, 30, 0), win(1, []int{2}, "19:00", "20:00"))
	same(t, h.at(tue(19, 30, 1)), "connect allstar>41234")
	same(t, h.at(tue(20, 0, 5)), "disconnect allstar>41234")
	// a window that already ended is not connected
	h = newHarness(t, tue(20, 30, 0), win(1, []int{2}, "19:00", "20:00"))
	same(t, h.at(tue(20, 30, 1)))
}

func TestCatchUpOnStartSkippedWhenLinked(t *testing.T) {
	h := newHarness(t, tue(19, 30, 0), win(1, []int{2}, "19:00", "20:00"))
	h.lk.links["allstar>41234"] = "linked"
	same(t, h.at(tue(19, 30, 1)))
	same(t, h.at(tue(20, 0, 5)), "disconnect allstar>41234")
}

func TestCatchUpOnReEnable(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.e.SetEnabled(false)
	same(t, h.at(tue(19, 0, 5)))
	same(t, h.at(tue(19, 10, 5)))
	h.clock(tue(19, 20, 0))
	h.e.SetEnabled(true)
	same(t, h.at(tue(19, 20, 1)), "connect allstar>41234")
	same(t, h.at(tue(20, 0, 5)), "disconnect allstar>41234")
}

func TestSingleNotReplayedOnReEnable(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(3, []int{2}, "19:00", ""))
	h.e.SetEnabled(false)
	same(t, h.at(tue(19, 0, 5)))
	h.clock(tue(19, 5, 0))
	h.e.SetEnabled(true)
	same(t, h.at(tue(19, 5, 10)))
}

func TestPauseCancelsAndIsNotCaughtUp(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	same(t, h.at(tue(19, 0, 5)), "connect allstar>41234")
	h.clock(tue(19, 10, 0))
	h.e.Pause("manual connect 41234 on 1997 by op")
	snap := h.e.Snapshot()
	if snap.Enabled || snap.PausedReason != "manual connect 41234 on 1997 by op" || snap.PausedAt == nil || !snap.PausedAt.Equal(tue(19, 10, 0)) {
		t.Fatalf("snapshot %+v", snap)
	}
	if len(snap.Active) != 0 {
		t.Fatal("active while paused")
	}
	if h.st.settings[KeyEnabled] != "false" || h.st.settings[KeyPausedReason] == "" {
		t.Fatalf("settings %v", h.st.settings)
	}
	same(t, h.at(tue(19, 11, 0)))
	h.clock(tue(19, 20, 0))
	h.e.SetEnabled(true)
	if s := h.e.Snapshot(); !s.Enabled || s.PausedReason != "" || s.PausedAt != nil {
		t.Fatalf("snapshot %+v", s)
	}
	same(t, h.at(tue(19, 20, 5)))
	same(t, h.at(tue(20, 0, 5))) // cancelled: no closing disconnect
	same(t, h.at(tue(21, 0, 5)))
	// the next occurrence runs normally
	delete(h.lk.links, "allstar>41234")
	same(t, h.at(tm(2026, 11, 24, 19, 0, 5)), "connect allstar>41234")
}

func TestPauseCancelsUnhandledRunningWindow(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.lk.setErr(errors.New("down"))
	h.at(tue(19, 0, 5))
	h.lk.setErr(nil)
	h.clock(tue(19, 5, 0))
	h.e.Pause("manual disconnect all on 1997 by op")
	h.clock(tue(19, 6, 0))
	h.e.SetEnabled(true)
	same(t, h.at(tue(19, 7, 0)))
}

func TestPauseKeepsFirstReasonAndWindowsStartedLaterRun(t *testing.T) {
	h := newHarness(t, tue(18, 0, 0), win(1, []int{2}, "19:00", "20:00"))
	h.e.Pause("first")
	h.e.Pause("second")
	if r := h.e.Snapshot().PausedReason; r != "first" {
		t.Fatal(r)
	}
	// not running at pause time: not cancelled, caught up after re-enable
	h.clock(tue(19, 30, 0))
	h.e.SetEnabled(true)
	same(t, h.at(tue(19, 30, 5)), "connect allstar>41234")
}

func TestStatePersistsAcrossRestartMissedEnd(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	same(t, h.at(tue(19, 0, 5)), "connect allstar>41234")
	// the panel restarts at 21:00 with the same store
	h.clock(tue(21, 0, 0))
	h.e = h.engine()
	same(t, h.at(tue(21, 0, 1)), "disconnect allstar>41234")
	same(t, h.at(tue(21, 1, 1)))
	// once more with the link already gone: no command
	h2 := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h2.at(tue(19, 0, 5))
	delete(h2.lk.links, "allstar>41234")
	h2.clock(tue(21, 0, 0))
	h2.e = h2.engine()
	same(t, h2.at(tue(21, 0, 1)))
}

func TestRestartMidWindowDoesNotReconnect(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.at(tue(19, 0, 5))
	h.clock(tue(19, 30, 0))
	h.e = h.engine()
	same(t, h.at(tue(19, 30, 1))) // handled: nothing to do
	same(t, h.at(tue(20, 0, 5)), "disconnect allstar>41234")
}

func TestRestartKeepsPause(t *testing.T) {
	h := newHarness(t, tue(18, 0, 0), win(1, []int{2}, "19:00", "20:00"))
	h.e.Pause("manual connect 41234 on 1997 by op")
	h.e = h.engine()
	s := h.e.Snapshot()
	if s.Enabled || s.PausedReason == "" {
		t.Fatalf("%+v", s)
	}
	same(t, h.at(tue(19, 0, 5)))
}

func TestOneOffDisablesAfterSingle(t *testing.T) {
	c := win(3, nil, "19:00", "")
	c.Date = "2026-11-17"
	h := newHarness(t, tue(18, 59, 50), c)
	same(t, h.at(tue(19, 0, 5)), "connect allstar>41234")
	if h.st.get(3).Enabled {
		t.Fatal("one-off still enabled")
	}
	// failing one-off stays enabled until its retries are over
	c2 := c
	c2.ID = 4
	h2 := newHarness(t, tue(18, 59, 50), c2)
	h2.lk.setErr(errors.New("down"))
	h2.at(tue(19, 0, 5))
	if !h2.st.get(4).Enabled {
		t.Fatal("disabled too early")
	}
	h2.at(tue(19, 11, 5))
	if h2.st.get(4).Enabled {
		t.Fatal("not disabled after the retries")
	}
}

func TestOneOffWindowDisablesAfterEnd(t *testing.T) {
	c := win(3, nil, "19:00", "20:00")
	c.Date = "2026-11-17"
	h := newHarness(t, tue(18, 59, 50), c)
	h.at(tue(19, 0, 5))
	if !h.st.get(3).Enabled {
		t.Fatal("disabled mid-window")
	}
	same(t, h.at(tue(20, 0, 5)), "disconnect allstar>41234")
	if h.st.get(3).Enabled {
		t.Fatal("still enabled after the end")
	}
}

func TestOneOffInThePastGetsDisabledWithoutCommands(t *testing.T) {
	c := win(3, nil, "19:00", "20:00")
	c.Date = "2026-01-05"
	h := newHarness(t, tue(18, 0, 0), c)
	same(t, h.at(tue(18, 0, 5)))
	if h.st.get(3).Enabled {
		t.Fatal("past one-off still enabled")
	}
}

func TestOneOffWindowNeverConnectedDisablesAtEnd(t *testing.T) {
	c := win(3, nil, "19:00", "19:02")
	c.Date = "2026-11-17"
	h := newHarness(t, tue(18, 59, 50), c)
	h.lk.setErr(errors.New("down"))
	h.at(tue(19, 0, 5))
	h.at(tue(19, 1, 6))
	if !h.st.get(3).Enabled {
		t.Fatal("disabled mid-window")
	}
	h.at(tue(19, 2, 7))
	if h.st.get(3).Enabled {
		t.Fatal("not disabled at the end")
	}
}

func TestEventOrderInSameMinute(t *testing.T) {
	a := win(1, []int{2}, "19:00", "20:00")
	a.Target = "40001"
	b := win(2, []int{2}, "20:00", "21:00")
	b.Target = "40002"
	c := Schedule{ID: 3, Enabled: true, Mode: "allstar", Target: "40003", Action: ActionDisconnect, Days: []int{2}, Start: "20:00"}
	d := win(4, []int{2}, "20:00", "")
	d.Target = "40004"
	h := newHarness(t, tue(18, 59, 50), a, b, c, d)
	h.lk.links["allstar>40003"] = "linked"
	h.at(tue(19, 0, 5))
	h.lk.take()
	got := h.at(tue(20, 0, 5))
	same(t, got,
		"disconnect allstar>40001", // window end first
		"disconnect allstar>40003", // then disconnect schedules
		"connect allstar>40002",    // then connects by id
		"connect allstar>40004")
}

func TestChangedForgetsOccurrenceAndCatchesUp(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.at(tue(19, 0, 5))
	h.clock(tue(19, 10, 0))
	h.e.Changed(1)
	// link still there: skipped, but the window still ends with a disconnect
	same(t, h.at(tue(19, 10, 5)))
	if r := h.st.get(1).LastResult; r != "skipped: already connected" {
		t.Fatalf("result %q", r)
	}
	same(t, h.at(tue(20, 0, 5)), "disconnect allstar>41234")
}

func TestChangedOnDisabledScheduleForgetsOccurrence(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.at(tue(19, 0, 5))
	s := h.st.get(1)
	s.Enabled = false
	h.st.set(s)
	h.e.Changed(1)
	same(t, h.at(tue(20, 0, 5)))
	if len(h.e.Snapshot().Active) != 0 {
		t.Fatal("active")
	}
}

func TestDisabledAndDeletedSchedulesDoNothing(t *testing.T) {
	s := win(1, []int{2}, "19:00", "20:00")
	s.Enabled = false
	h := newHarness(t, tue(18, 59, 50), s)
	same(t, h.at(tue(19, 0, 5)))
	same(t, h.at(tue(19, 30, 5)))
}

func TestMidnightCrossingWindowRuns(t *testing.T) {
	h := newHarness(t, tm(2026, 11, 20, 22, 59, 50), win(1, []int{5}, "23:00", "01:00")) // Friday
	same(t, h.at(tm(2026, 11, 20, 23, 0, 5)), "connect allstar>41234")
	same(t, h.at(tm(2026, 11, 21, 0, 30, 0)))
	same(t, h.at(tm(2026, 11, 21, 1, 0, 5)), "disconnect allstar>41234")
}

func TestMidnightWindowCatchUpAfterMidnight(t *testing.T) {
	// started at 00:30 Saturday: Friday's window is running
	h := newHarness(t, tm(2026, 11, 21, 0, 30, 0), win(1, []int{5}, "23:00", "01:00"))
	same(t, h.at(tm(2026, 11, 21, 0, 30, 1)), "connect allstar>41234")
}

func TestHandlerCommandsDoNotRunWhilePaused(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.e.SetEnabled(false)
	if h.st.settings[KeyEnabled] != "false" {
		t.Fatal("not persisted")
	}
	same(t, h.at(tue(19, 0, 5)))
	same(t, h.at(tue(20, 0, 5)))
}

func TestOnChangeCalled(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	h.at(tue(19, 0, 5))
	if h.chgs == 0 {
		t.Fatal("no change notification after an executed event")
	}
	n := h.chgs
	h.e.SetEnabled(false)
	h.e.Pause("x")
	h.e.Changed(1)
	if h.chgs != n+3 {
		t.Fatalf("changes %d -> %d", n, h.chgs)
	}
}

func TestSnapshot(t *testing.T) {
	a := win(1, []int{2}, "19:00", "20:30")
	a.Name = "Tuesday net"
	b := win(2, []int{3}, "07:00", "")
	b.Mode = "aaa"
	h := newHarness(t, tue(18, 42, 0), a, b)
	snap := h.e.Snapshot()
	if !snap.Enabled || snap.Timezone != "America/New_York" || snap.Now.Format(time.RFC3339) != "2026-11-17T18:42:00-05:00" {
		t.Fatalf("%+v", snap)
	}
	if len(snap.Schedules) != 2 || snap.Schedules[0].ID != 2 { // node 1996 sorts first
		t.Fatalf("schedules %+v", snap.Schedules)
	}
	if len(snap.Upcoming) == 0 || snap.Upcoming[0].ID != 1 || snap.Upcoming[0].Action != "connect" ||
		snap.Upcoming[0].At.Format(time.RFC3339) != "2026-11-17T19:00:00-05:00" || snap.Upcoming[0].Name != "Tuesday net" {
		t.Fatalf("upcoming %+v", snap.Upcoming)
	}
	h.e.SetEnabled(false)
	if len(h.e.Snapshot().Upcoming) == 0 {
		t.Fatal("upcoming must be computed while paused")
	}
}
