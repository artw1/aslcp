package schedules

import (
	"errors"
	"testing"
	"time"
)

const (
	cTue = "connect 1997>41234 transceive perm=false excl=false"
	dTue = "disconnect 1997>41234 perm=false"
	cOn8 = "connect 1998>41234 transceive perm=false excl=false"
	dOn8 = "disconnect 1998>41234 perm=false"
)

func (h *harness) mode(m string) {
	h.t.Helper()
	if err := h.e.SetManualAction(m); err != nil {
		h.t.Fatal(err)
	}
}

func on1998(s Schedule) Schedule { s.Node = "1998"; return s }

func TestSetManualAction(t *testing.T) {
	h := newHarness(t, tue(12, 0, 0))
	if h.e.ManualActionMode() != ManualPause || h.e.Snapshot().ManualAction != ManualPause {
		t.Fatal("default must be pause")
	}
	if err := h.e.SetManualAction("bogus"); err == nil {
		t.Fatal("bogus accepted")
	}
	h.mode(ManualCancel)
	if h.st.settings[KeyManualAction] != ManualCancel || h.e.Snapshot().ManualAction != ManualCancel {
		t.Fatalf("%v", h.st.settings)
	}
	h.e = h.engine() // restart
	if h.e.ManualActionMode() != ManualCancel {
		t.Fatal("not persisted")
	}
	h.st.settings[KeyManualAction] = "garbage"
	h.e = h.engine()
	if h.e.ManualActionMode() != ManualPause {
		t.Fatal("unknown value must fall back to pause")
	}
}

func TestManualActionPauseMode(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"))
	same(t, h.at(tue(19, 0, 5)), cTue)
	h.clock(tue(19, 10, 0))
	h.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	s := h.e.Snapshot()
	if s.Enabled || s.ResumeAt != nil || s.PausedReason == "" || s.LastCancel != nil {
		t.Fatalf("%+v", s)
	}
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5))) // never resumes by itself
	if h.e.Enabled() {
		t.Fatal("resumed")
	}
}

func TestPauseTodayResumesAtMidnight(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"), win(2, []int{3}, "00:30", "01:30"))
	h.mode(ManualPauseToday)
	same(t, h.at(tue(19, 0, 5)), cTue)
	h.clock(tue(19, 10, 0))
	h.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	s := h.e.Snapshot()
	want := tm(2026, 11, 18, 0, 0, 0)
	if s.Enabled || s.ResumeAt == nil || !s.ResumeAt.Equal(want) || s.PausedReason == "" {
		t.Fatalf("%+v", s)
	}
	if h.st.settings[KeyResumeAt] != want.Format(time.RFC3339) {
		t.Fatalf("settings %v", h.st.settings)
	}
	changes := h.chgs
	same(t, h.at(tue(20, 0, 5))) // cancelled window: no closing disconnect
	same(t, h.at(tue(23, 59, 50)))
	if h.e.Enabled() {
		t.Fatal("resumed early")
	}
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5)))
	s = h.e.Snapshot()
	if !s.Enabled || s.PausedReason != "" || s.PausedAt != nil || s.ResumeAt != nil {
		t.Fatalf("after midnight %+v", s)
	}
	if h.st.settings[KeyEnabled] != "true" || h.st.settings[KeyResumeAt] != "" || h.st.settings[KeyPausedReason] != "" {
		t.Fatalf("settings %v", h.st.settings)
	}
	if h.chgs == changes {
		t.Fatal("OnChange not called on resume")
	}
	// the window that starts after midnight runs normally
	delete(h.lk.links, "1997>41234")
	same(t, h.at(tm(2026, 11, 18, 0, 30, 5)), cTue)
	same(t, h.at(tm(2026, 11, 18, 1, 30, 5)), dTue)
}

func TestPauseTodayCrossingWindowStaysCancelled(t *testing.T) {
	h := newHarness(t, tue(22, 59, 50), win(1, []int{2}, "23:00", "01:00"))
	h.mode(ManualPauseToday)
	same(t, h.at(tue(23, 0, 5)), cTue)
	h.clock(tue(23, 30, 0))
	h.e.ManualAction("1997", "manual disconnect all on 1997 by op")
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5))) // resumed, window cancelled: not caught up
	if !h.e.Enabled() {
		t.Fatal("not resumed")
	}
	same(t, h.at(tm(2026, 11, 18, 0, 30, 0)))
	same(t, h.at(tm(2026, 11, 18, 1, 0, 5))) // no closing disconnect either
}

func TestPauseTodayRunsSingleEventAtMidnight(t *testing.T) {
	s := win(1, []int{3}, "00:00", "")
	s.Mode = ModeMonitor
	h := newHarness(t, tue(23, 0, 0), s)
	h.mode(ManualPauseToday)
	h.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	same(t, h.at(tue(23, 59, 50)))
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5)), "connect 1997>41234 monitor perm=false excl=false")
}

func TestPauseTodaySingleEventsDuringPauseNotReplayed(t *testing.T) {
	h := newHarness(t, tue(18, 0, 0), win(1, []int{2}, "19:00", ""))
	h.mode(ManualPauseToday)
	h.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	same(t, h.at(tue(19, 0, 5)))
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5)))
	same(t, h.at(tm(2026, 11, 18, 0, 1, 5)))
}

func TestManualReEnableClearsResumeAt(t *testing.T) {
	h := newHarness(t, tue(12, 0, 0), win(1, []int{2}, "19:00", "20:00"))
	h.mode(ManualPauseToday)
	h.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	if h.e.Snapshot().ResumeAt == nil {
		t.Fatal("no resumeAt")
	}
	h.e.SetEnabled(true)
	if s := h.e.Snapshot(); !s.Enabled || s.ResumeAt != nil || h.st.settings[KeyResumeAt] != "" {
		t.Fatalf("%+v %v", s, h.st.settings)
	}
	// switching off by hand later is an indefinite pause
	h.e.SetEnabled(false)
	if s := h.e.Snapshot(); s.Enabled || s.ResumeAt != nil {
		t.Fatalf("%+v", s)
	}
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5)))
	if h.e.Enabled() {
		t.Fatal("resumed")
	}
}

func TestPauseTodayNeverShortensIndefinitePause(t *testing.T) {
	h := newHarness(t, tue(12, 0, 0), win(1, []int{2}, "19:00", "20:00"))
	h.e.SetEnabled(false) // paused from the switch
	h.mode(ManualPauseToday)
	h.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	if s := h.e.Snapshot(); s.Enabled || s.ResumeAt != nil {
		t.Fatalf("%+v", s)
	}
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5)))
	if h.e.Enabled() {
		t.Fatal("resumed")
	}
	// paused for good by a pause-mode action, then a pause_today action
	h2 := newHarness(t, tue(12, 0, 0))
	h2.e.ManualAction("1997", "first")
	h2.mode(ManualPauseToday)
	h2.e.ManualAction("1997", "second")
	if s := h2.e.Snapshot(); s.Enabled || s.ResumeAt != nil || s.PausedReason != "first" {
		t.Fatalf("%+v", s)
	}
}

func TestPauseModeClearsResumeAt(t *testing.T) {
	h := newHarness(t, tue(12, 0, 0))
	h.mode(ManualPauseToday)
	h.e.ManualAction("1997", "first")
	if h.e.Snapshot().ResumeAt == nil {
		t.Fatal("no resumeAt")
	}
	h.mode(ManualPause)
	h.e.ManualAction("1997", "second")
	if s := h.e.Snapshot(); s.Enabled || s.ResumeAt != nil || s.PausedReason != "first" || h.st.settings[KeyResumeAt] != "" {
		t.Fatalf("%+v %v", s, h.st.settings)
	}
	// pause_today after pause_today keeps the first resume time
	h3 := newHarness(t, tue(12, 0, 0))
	h3.mode(ManualPauseToday)
	h3.e.ManualAction("1997", "first")
	h3.clock(tue(20, 0, 0))
	h3.e.ManualAction("1997", "second")
	if s := h3.e.Snapshot(); s.ResumeAt == nil || !s.ResumeAt.Equal(tm(2026, 11, 18, 0, 0, 0)) {
		t.Fatalf("%+v", s)
	}
}

func TestResumeAtSurvivesRestart(t *testing.T) {
	h := newHarness(t, tue(12, 0, 0), win(1, []int{3}, "00:30", "01:30"))
	h.mode(ManualPauseToday)
	h.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	h.clock(tue(20, 0, 0))
	h.e = h.engine() // restart before midnight
	s := h.e.Snapshot()
	if s.Enabled || s.ResumeAt == nil || !s.ResumeAt.Equal(tm(2026, 11, 18, 0, 0, 0)) {
		t.Fatalf("%+v", s)
	}
	same(t, h.at(tue(23, 59, 0)))
	same(t, h.at(tm(2026, 11, 18, 0, 0, 5)))
	if !h.e.Enabled() {
		t.Fatal("not resumed")
	}
	// restart after midnight with the pause still stored: resumes on the first tick
	h2 := newHarness(t, tue(12, 0, 0))
	h2.mode(ManualPauseToday)
	h2.e.ManualAction("1997", "manual connect 2000 on 1997 by op")
	h2.clock(tm(2026, 11, 18, 8, 0, 0))
	h2.e = h2.engine()
	same(t, h2.at(tm(2026, 11, 18, 8, 0, 1)))
	if s := h2.e.Snapshot(); !s.Enabled || s.ResumeAt != nil {
		t.Fatalf("%+v", s)
	}
}

func TestCancelModeOnlyThatNode(t *testing.T) {
	a := win(1, []int{2}, "19:00", "20:00")
	b := on1998(win(2, []int{2}, "19:00", "20:00"))
	a.Name = "Tuesday net"
	h := newHarness(t, tue(18, 59, 50), a, b)
	h.mode(ManualCancel)
	same(t, h.at(tue(19, 0, 5)), cTue, cOn8)
	h.clock(tue(19, 10, 0))
	h.e.ManualAction("1997", "manual connect 41234 on 1997 by op")
	s := h.e.Snapshot()
	if !s.Enabled || s.PausedReason != "" || s.ResumeAt != nil {
		t.Fatalf("schedules must stay on: %+v", s)
	}
	if len(s.Active) != 1 || s.Active[0].ID != 2 {
		t.Fatalf("active %+v", s.Active)
	}
	n := s.LastCancel
	if n == nil || n.Reason != "manual connect 41234 on 1997 by op" || len(n.IDs) != 1 || n.IDs[0] != 1 ||
		len(n.Names) != 1 || n.Names[0] != "Tuesday net" || !n.At.Equal(tue(19, 10, 0)) || !n.Until.Equal(tue(20, 0, 0)) {
		t.Fatalf("notice %+v", n)
	}
	if h.st.settings[KeyLastCancel] == "" {
		t.Fatal("notice not persisted")
	}
	// node 1997's window does not disconnect, node 1998's does
	same(t, h.at(tue(20, 0, 5)), dOn8)
	// the next occurrence on 1997 runs normally
	delete(h.lk.links, "1997>41234")
	same(t, h.at(tm(2026, 11, 24, 19, 0, 5)), cTue, cOn8)
}

func TestCancelModeNothingRunningOnNodeDoesNothing(t *testing.T) {
	b := on1998(win(2, []int{2}, "19:00", "20:00"))
	h := newHarness(t, tue(18, 59, 50), b, win(1, []int{2}, "21:00", "22:00"))
	h.mode(ManualCancel)
	same(t, h.at(tue(19, 0, 5)), cOn8)
	h.clock(tue(19, 10, 0))
	h.e.ManualAction("1997", "manual connect 41234 on 1997 by op") // 1997's window starts later
	s := h.e.Snapshot()
	if s.LastCancel != nil || !s.Enabled || len(s.Active) != 1 || h.st.settings[KeyLastCancel] != "" {
		t.Fatalf("%+v", s)
	}
	same(t, h.at(tue(20, 0, 5)), dOn8)
	same(t, h.at(tue(21, 0, 5)), cTue) // not cancelled
}

func TestCancelModeCancelsUnhandledAndSinglesRetries(t *testing.T) {
	single := win(2, []int{2}, "19:00", "")
	single.Target = "50241"
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"), single)
	h.mode(ManualCancel)
	h.lk.setErr(errors.New("down"))
	h.at(tue(19, 0, 5)) // both fail and are retried
	h.lk.setErr(nil)
	h.clock(tue(19, 2, 0))
	h.e.ManualAction("1997", "manual disconnect all on 1997 by op")
	n := h.e.Snapshot().LastCancel
	if n == nil || len(n.IDs) != 2 {
		t.Fatalf("notice %+v", n)
	}
	same(t, h.at(tue(19, 3, 0))) // no retries
	same(t, h.at(tue(19, 15, 0)))
	same(t, h.at(tue(20, 0, 5)))
}

func TestCancelNoticeExpiresAndSurvivesRestart(t *testing.T) {
	a := win(1, []int{2}, "19:00", "20:00")
	h := newHarness(t, tue(18, 59, 50), a)
	h.mode(ManualCancel)
	h.at(tue(19, 0, 5))
	h.clock(tue(19, 10, 0))
	h.e.ManualAction("1997", "manual connect 41234 on 1997 by op")
	h.e = h.engine() // restart
	h.clock(tue(19, 30, 0))
	if h.e.Snapshot().LastCancel == nil {
		t.Fatal("notice lost over restart")
	}
	same(t, h.at(tue(19, 59, 0)))
	if h.e.Snapshot().LastCancel == nil {
		t.Fatal("notice dropped early")
	}
	h.at(tue(20, 0, 5))
	if h.e.Snapshot().LastCancel != nil {
		t.Fatal("notice shown after the window's end")
	}
	if h.st.settings[KeyLastCancel] != "" {
		t.Fatalf("persisted notice not cleared: %q", h.st.settings[KeyLastCancel])
	}
}

func TestCancelNoticeDroppedWhenScheduleEditedOrDisabled(t *testing.T) {
	h := newHarness(t, tue(18, 59, 50), win(1, []int{2}, "19:00", "20:00"), win(2, []int{2}, "19:00", "20:00"))
	h.mode(ManualCancel)
	h.at(tue(19, 0, 5))
	h.clock(tue(19, 10, 0))
	h.e.ManualAction("1997", "manual connect 41234 on 1997 by op")
	if n := h.e.Snapshot().LastCancel; n == nil || len(n.IDs) != 2 {
		t.Fatalf("%+v", n)
	}
	h.e.Changed(1) // edited: its occurrence is forgotten
	if n := h.e.Snapshot().LastCancel; n == nil || len(n.IDs) != 1 || n.IDs[0] != 2 {
		t.Fatalf("%+v", n)
	}
	s2 := h.st.get(2)
	s2.Enabled = false
	h.st.set(s2)
	if n := h.e.Snapshot().LastCancel; n != nil {
		t.Fatalf("notice for a disabled schedule: %+v", n)
	}
}

func TestManualActionOnChangeAndNoOp(t *testing.T) {
	h := newHarness(t, tue(12, 0, 0))
	h.mode(ManualCancel)
	before := h.chgs
	h.e.ManualAction("1997", "manual connect 41234 on 1997 by op")
	if h.chgs != before {
		t.Fatal("a no-op cancel must not notify")
	}
}
