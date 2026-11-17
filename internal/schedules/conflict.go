package schedules

import (
	"fmt"
	"strings"
	"time"
)

const (
	dayMin  = 24 * 60
	weekMin = 7 * dayMin
)

// iv is a span in minutes; an instant has instant set and e == s.
type iv struct {
	s, e    int
	instant bool
}

// dateNum returns the day number (days since 1970-01-01) of a YYYY-MM-DD date.
func dateNum(d string) (int, bool) {
	t, err := time.Parse("2006-01-02", d)
	if err != nil {
		return 0, false
	}
	return int(t.Unix() / 86400), true
}

// weekdayOf returns the weekday of a day number (0 = Sunday); 1970-01-01 was
// a Thursday.
func weekdayOf(n int) int { return ((n+4)%7 + 7) % 7 }

// span returns the interval of s that starts on a day at offset base
// (minutes).
func span(s Schedule, base int) iv {
	st := base + clockMinutes(s.Start)
	if s.End == "" {
		return iv{s: st, e: st, instant: true}
	}
	d := clockMinutes(s.End) - clockMinutes(s.Start)
	if d <= 0 {
		d += dayMin
	}
	return iv{s: st, e: st + d}
}

func overlap(a, b iv) bool {
	switch {
	case a.instant && b.instant:
		return false
	case a.instant:
		return b.s <= a.s && a.s < b.e
	case b.instant:
		return a.s <= b.s && b.s < a.e
	}
	return a.s < b.e && b.s < a.e
}

func shift(a iv, by int) iv { return iv{a.s + by, a.e + by, a.instant} }

func effExclusive(s Schedule) bool { return s.Exclusive && s.Mode != ModeLocalMonitor }

// weeklySpans lists the spans of a weekly schedule on the week circle.
func weeklySpans(s Schedule) []iv {
	out := make([]iv, 0, len(s.Days))
	for _, d := range s.Days {
		if d >= 0 && d <= 6 {
			out = append(out, span(s, d*dayMin))
		}
	}
	return out
}

// onDates lists the spans of a weekly schedule starting on the absolute day
// numbers n-1 .. n+1 (minutes since the epoch).
func onDates(s Schedule, n int) []iv {
	var out []iv
	for m := n - 1; m <= n+1; m++ {
		for _, d := range s.Days {
			if d == weekdayOf(m) {
				out = append(out, span(s, m*dayMin))
			}
		}
	}
	return out
}

// pairOverlap reports whether two connect schedules overlap on some date.
func pairOverlap(a, b Schedule) bool {
	switch {
	case a.Date == "" && b.Date == "":
		for _, x := range weeklySpans(a) {
			for _, y := range weeklySpans(b) {
				for _, sh := range []int{-weekMin, 0, weekMin} {
					if overlap(x, shift(y, sh)) {
						return true
					}
				}
			}
		}
	case a.Date != "" && b.Date != "":
		na, ok1 := dateNum(a.Date)
		nb, ok2 := dateNum(b.Date)
		return ok1 && ok2 && overlap(span(a, na*dayMin), span(b, nb*dayMin))
	case a.Date != "":
		return pairOverlap(b, a)
	default: // a weekly, b once
		n, ok := dateNum(b.Date)
		if !ok {
			return false
		}
		x := span(b, n*dayMin)
		for _, y := range onDates(a, n) {
			if overlap(x, y) {
				return true
			}
		}
	}
	return false
}

// Conflicts returns the schedules in others that s conflicts with (see
// docs/SCHEDULES.md, "Overlap check"): same node, both connects, both
// enabled, at least one exclusive, and overlapping on some actual date.
// Wall-clock minutes are compared, so DST edge cases are ignored. An entry
// of others with the same ID as s is skipped (s being saved over it).
func Conflicts(s Schedule, others []Schedule) []Schedule {
	var out []Schedule
	if s.Action != ActionConnect || !s.Enabled {
		return out
	}
	for _, o := range others {
		if o.ID == s.ID && s.ID != 0 {
			continue
		}
		if o.Node != s.Node || o.Action != ActionConnect || !o.Enabled {
			continue
		}
		if !effExclusive(s) && !effExclusive(o) {
			continue
		}
		if pairOverlap(s, o) {
			out = append(out, o)
		}
	}
	return out
}

var dayNames = []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}

// daysText formats weekdays like the UI: "Daily", "Mon–Fri", "Tue, Thu".
func daysText(days []int) string {
	set := map[int]bool{}
	for _, d := range days {
		set[d] = true
	}
	if len(set) == 7 {
		return "Daily"
	}
	order := []int{1, 2, 3, 4, 5, 6, 0}
	var idx []int
	for i, d := range order {
		if set[d] {
			idx = append(idx, i)
		}
	}
	var out []string
	for i := 0; i < len(idx); {
		j := i
		for j+1 < len(idx) && idx[j+1] == idx[j]+1 {
			j++
		}
		if j-i >= 2 {
			out = append(out, dayNames[order[idx[i]]]+"–"+dayNames[order[idx[j]]])
		} else {
			for k := i; k <= j; k++ {
				out = append(out, dayNames[order[idx[k]]])
			}
		}
		i = j + 1
	}
	return strings.Join(out, ", ")
}

// When describes when s runs: "Tue 19:00–20:30", "2026-12-01 19:00–21:00".
func When(s Schedule) string {
	day := s.Date
	if day == "" {
		day = daysText(s.Days)
	}
	w := day + " " + s.Start
	if s.End != "" {
		w += "–" + s.End
	}
	return w
}

// ConflictMessage names the clashing schedules for the user, for example
// `overlaps "Tuesday net" (Tue 19:00–20:30) on node 2006`.
func ConflictMessage(node string, clash []Schedule) string {
	parts := make([]string, len(clash))
	for i, c := range clash {
		name := fmt.Sprintf("#%d", c.ID)
		if c.Name != "" {
			name = fmt.Sprintf("%q", c.Name)
		}
		parts[i] = fmt.Sprintf("%s (%s)", name, When(c))
	}
	list := strings.Join(parts, ", ")
	if n := len(parts); n > 1 {
		list = strings.Join(parts[:n-1], ", ") + " and " + parts[n-1]
	}
	return fmt.Sprintf("overlaps %s on node %s", list, node)
}
