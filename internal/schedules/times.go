package schedules

import (
	"sort"
	"time"
)

// Occ is a tracked occurrence of a window: Status is "connected" (the
// scheduler handled the connect, the end will disconnect) or "cancelled" (a
// manual action took over; nothing more happens for it).
type Occ struct {
	ID     int64     `json:"id"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Status string    `json:"status"`
}

// Occurrence states.
const (
	StatusConnected = "connected"
	StatusCancelled = "cancelled"
)

// Event is one upcoming action (API shape).
type Event struct {
	At     time.Time `json:"at"`
	ID     int64     `json:"id"`
	Name   string    `json:"name"`
	Mode   string    `json:"mode"`
	Target string    `json:"target"`
	Action string    `json:"action"`
}

// Horizon is how far ahead Upcoming looks.
const Horizon = 8 * 24 * time.Hour

// civil returns the instant of minute-of-day mins (may exceed 1439: the next
// day) on the civil date of day in loc. A wall time that does not exist (DST
// gap) yields the first valid minute after it; a wall time that exists twice
// yields the first occurrence (time.Date's normalisation).
func civil(day time.Time, mins int, loc *time.Location) time.Time {
	y, m, d := day.Date()
	for i := 0; i < 26*60; i++ {
		mm := mins + i
		c := time.Date(y, m, d, mm/60, mm%60, 0, 0, loc)
		w := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Add(time.Duration(mm) * time.Minute)
		cy, cm, cd := c.Date()
		if cy == w.Year() && cm == w.Month() && cd == w.Day() && c.Hour() == w.Hour() && c.Minute() == w.Minute() {
			return c
		}
	}
	return time.Date(y, m, d, mins/60, mins%60, 0, 0, loc)
}

type occurrence struct {
	s          Schedule
	start, end time.Time // end is zero without an end time
}

// occurrences returns the occurrences of s whose start is in [lo, hi].
func occurrences(s Schedule, lo, hi time.Time, loc *time.Location) []occurrence {
	var out []occurrence
	smin := clockMinutes(s.Start)
	day := time.Date(lo.In(loc).Year(), lo.In(loc).Month(), lo.In(loc).Day(), 12, 0, 0, 0, loc)
	last := hi.In(loc)
	for ; !day.After(last.AddDate(0, 0, 1)); day = day.AddDate(0, 0, 1) {
		if s.Date != "" {
			if day.Format("2006-01-02") != s.Date {
				continue
			}
		} else {
			wd := int(day.Weekday())
			ok := false
			for _, d := range s.Days {
				if d == wd {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		st := civil(day, smin, loc)
		if st.Before(lo) || st.After(hi) {
			continue
		}
		o := occurrence{s: s, start: st}
		if s.isWindow() {
			emin := clockMinutes(s.End)
			if emin < smin {
				emin += 24 * 60
			}
			o.end = civil(day, emin, loc)
			if !o.end.After(o.start) {
				o.end = o.start.Add(time.Minute)
			}
		}
		out = append(out, o)
	}
	return out
}

// event kinds in execution order for events due in the same minute.
const (
	rankEnd = iota
	rankDisconnect
	rankConnect
)

type ordered struct {
	ev   Event
	rank int
}

func sortOrdered(l []ordered) {
	sort.SliceStable(l, func(i, j int) bool {
		a, b := l[i], l[j]
		am, bm := a.ev.At.Truncate(time.Minute), b.ev.At.Truncate(time.Minute)
		if !am.Equal(bm) {
			return am.Before(bm)
		}
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		return a.ev.ID < b.ev.ID
	})
}

// Upcoming returns the next n events after now, within Horizon, sorted in
// execution order. Disabled schedules and cancelled occurrences yield none.
func Upcoming(list []Schedule, state []Occ, now time.Time, loc *time.Location, n int) []Event {
	if loc == nil {
		loc = time.Local
	}
	cancelled := map[[2]int64]bool{}
	for _, o := range state {
		if o.Status == StatusCancelled {
			cancelled[[2]int64{o.ID, o.Start.Unix()}] = true
		}
	}
	var all []ordered
	for _, s := range list {
		if !s.Enabled {
			continue
		}
		for _, o := range occurrences(s, now.Add(-48*time.Hour), now.Add(Horizon), loc) {
			if cancelled[[2]int64{s.ID, o.start.Unix()}] {
				continue
			}
			mk := func(at time.Time, action string) Event {
				return Event{At: at.In(loc), ID: s.ID, Name: s.Name, Mode: s.Mode, Target: s.Target, Action: action}
			}
			rank := rankConnect
			if s.Action == ActionDisconnect {
				rank = rankDisconnect
			}
			if o.start.After(now) && !o.start.After(now.Add(Horizon)) {
				all = append(all, ordered{mk(o.start, s.Action), rank})
			}
			if !o.end.IsZero() && o.end.After(now) && !o.end.After(now.Add(Horizon)) {
				all = append(all, ordered{mk(o.end, ActionDisconnect), rankEnd})
			}
		}
	}
	sortOrdered(all)
	if n > 0 && len(all) > n {
		all = all[:n]
	}
	out := make([]Event, len(all))
	for i, o := range all {
		out[i] = o.ev
	}
	return out
}
