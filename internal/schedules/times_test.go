package schedules

import (
	"fmt"
	"testing"
	"time"
)

func evs(l []Event) []string {
	var out []string
	for _, e := range l {
		out = append(out, fmt.Sprintf("%s %s #%d", e.At.Format("2006-01-02 15:04 -0700"), e.Action, e.ID))
	}
	return out
}

func wantEvs(t *testing.T, got []Event, want ...string) {
	t.Helper()
	g := evs(got)
	if fmt.Sprint(g) != fmt.Sprint(want) {
		t.Fatalf("events:\n got  %q\n want %q", g, want)
	}
}

func TestUpcomingWeekly(t *testing.T) {
	s := win(1, []int{2, 4}, "19:00", "20:30") // Tue, Thu
	got := Upcoming([]Schedule{s}, nil, tue(18, 0, 0), ny, 10)
	wantEvs(t, got,
		"2026-11-17 19:00 -0500 connect #1", "2026-11-17 20:30 -0500 disconnect #1",
		"2026-11-19 19:00 -0500 connect #1", "2026-11-19 20:30 -0500 disconnect #1",
		"2026-11-24 19:00 -0500 connect #1", "2026-11-24 20:30 -0500 disconnect #1")
	if got[0].Mode != "allstar" || got[1].Mode != "allstar" {
		t.Fatalf("modes %+v", got[:2])
	}
	// limit n
	wantEvs(t, Upcoming([]Schedule{s}, nil, tue(18, 0, 0), ny, 2),
		"2026-11-17 19:00 -0500 connect #1", "2026-11-17 20:30 -0500 disconnect #1")
	// running window: only its end is upcoming
	wantEvs(t, Upcoming([]Schedule{s}, nil, tue(19, 30, 0), ny, 2),
		"2026-11-17 20:30 -0500 disconnect #1", "2026-11-19 19:00 -0500 connect #1")
}

func TestUpcomingHorizon(t *testing.T) {
	s := win(1, []int{2}, "19:00", "") // Tuesday only
	// from Tuesday 19:30 the next one is in exactly 7 d - 30 min: inside 8 days
	wantEvs(t, Upcoming([]Schedule{s}, nil, tue(19, 30, 0), ny, 10), "2026-11-24 19:00 -0500 connect #1")
	// a monthly-ish once-date beyond 8 days is not listed
	o := win(2, nil, "19:00", "")
	o.Date = "2026-11-30"
	wantEvs(t, Upcoming([]Schedule{o}, nil, tue(18, 0, 0), ny, 10))
}

func TestUpcomingMidnightCrossing(t *testing.T) {
	s := win(1, []int{5}, "23:00", "01:00") // Friday night
	// Saturday 00:30: Friday's window is running, its end is upcoming
	got := Upcoming([]Schedule{s}, nil, tm(2026, 11, 21, 0, 30, 0), ny, 10)
	wantEvs(t, got, "2026-11-21 01:00 -0500 disconnect #1", "2026-11-27 23:00 -0500 connect #1", "2026-11-28 01:00 -0500 disconnect #1")
	// Friday 22:00: both events of tonight's window
	got = Upcoming([]Schedule{s}, nil, tm(2026, 11, 20, 22, 0, 0), ny, 2)
	wantEvs(t, got, "2026-11-20 23:00 -0500 connect #1", "2026-11-21 01:00 -0500 disconnect #1")
}

func TestUpcomingOneOff(t *testing.T) {
	o := win(1, nil, "19:00", "21:00")
	o.Date = "2026-11-18"
	wantEvs(t, Upcoming([]Schedule{o}, nil, tue(12, 0, 0), ny, 10),
		"2026-11-18 19:00 -0500 connect #1", "2026-11-18 21:00 -0500 disconnect #1")
	o.Date = "2026-11-16" // yesterday
	wantEvs(t, Upcoming([]Schedule{o}, nil, tue(12, 0, 0), ny, 10))
	o.Date = "2020-01-01"
	wantEvs(t, Upcoming([]Schedule{o}, nil, tue(12, 0, 0), ny, 10))
	// today, already over
	o.Date = "2026-11-17"
	wantEvs(t, Upcoming([]Schedule{o}, nil, tue(22, 0, 0), ny, 10))
}

func TestUpcomingDisabledAndCancelled(t *testing.T) {
	s := win(1, []int{2}, "19:00", "20:30")
	d := s
	d.ID, d.Enabled = 2, false
	cancelled := []Occ{{ID: 1, Start: tue(19, 0, 0), End: tue(20, 30, 0), Status: StatusCancelled}}
	got := Upcoming([]Schedule{s, d}, cancelled, tue(19, 30, 0), ny, 10)
	wantEvs(t, got, "2026-11-24 19:00 -0500 connect #1", "2026-11-24 20:30 -0500 disconnect #1")
	// connected occurrences still list their end
	conn := []Occ{{ID: 1, Start: tue(19, 0, 0), End: tue(20, 30, 0), Status: StatusConnected}}
	wantEvs(t, Upcoming([]Schedule{s}, conn, tue(19, 30, 0), ny, 1), "2026-11-17 20:30 -0500 disconnect #1")
}

func TestUpcomingOrderSameMinute(t *testing.T) {
	a := win(1, []int{2}, "19:00", "20:00")
	b := win(2, []int{2}, "20:00", "21:00")
	c := Schedule{ID: 3, Enabled: true, Mode: "allstar", Target: "40003", Action: ActionDisconnect, Days: []int{2}, Start: "20:00"}
	got := Upcoming([]Schedule{b, c, a}, nil, tue(19, 30, 0), ny, 3)
	wantEvs(t, got, "2026-11-17 20:00 -0500 disconnect #1", "2026-11-17 20:00 -0500 disconnect #3", "2026-11-17 20:00 -0500 connect #2")
}

func TestUpcomingDSTGap(t *testing.T) {
	// 2026-03-08 02:00 -> 03:00 in New York: 02:30 does not exist
	s := win(1, []int{0}, "02:30", "") // Sunday
	got := Upcoming([]Schedule{s}, nil, tm(2026, 3, 7, 12, 0, 0), ny, 1)
	wantEvs(t, got, "2026-03-08 03:00 -0400 connect #1")
	// a window ending inside the gap ends at the first valid minute
	w := win(2, []int{0}, "01:00", "02:30")
	got = Upcoming([]Schedule{w}, nil, tm(2026, 3, 7, 12, 0, 0), ny, 2)
	wantEvs(t, got, "2026-03-08 01:00 -0500 connect #2", "2026-03-08 03:00 -0400 disconnect #2")
	// the week before is unaffected
	got = Upcoming([]Schedule{s}, nil, tm(2026, 2, 28, 12, 0, 0), ny, 1)
	wantEvs(t, got, "2026-03-01 02:30 -0500 connect #1")
}

func TestUpcomingDSTRepeatedHour(t *testing.T) {
	// 2026-11-01 02:00 EDT -> 01:00 EST: 01:30 happens twice, runs once, first
	s := win(1, []int{0}, "01:30", "")
	got := Upcoming([]Schedule{s}, nil, tm(2026, 10, 31, 12, 0, 0), ny, 3)
	wantEvs(t, got, "2026-11-01 01:30 -0400 connect #1", "2026-11-08 01:30 -0500 connect #1")
}

func TestDSTGapEngineRunsAtThree(t *testing.T) {
	s := win(1, []int{0}, "02:30", "")
	h := newHarness(t, tm(2026, 3, 8, 1, 59, 0), s)
	same(t, h.at(tm(2026, 3, 8, 3, 0, 0).Add(-time.Second)))
	same(t, h.at(tm(2026, 3, 8, 3, 0, 5)), "connect allstar>41234")
}
