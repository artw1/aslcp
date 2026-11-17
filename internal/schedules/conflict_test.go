package schedules

import (
	"strings"
	"testing"
)

func cs(id int64, days []int, date, start, end string, excl bool) Schedule {
	return Schedule{ID: id, Enabled: true, Node: "1997", Target: "41234", Action: ActionConnect, Mode: ModeTransceive,
		Exclusive: excl, Days: days, Date: date, Start: start, End: end}
}

func TestConflicts(t *testing.T) {
	// 2026-11-17 is a Tuesday, 2026-11-21 a Saturday, 2026-11-22 a Sunday.
	sat := []int{6}
	sun := []int{0}
	tuesday := []int{2}
	tests := []struct {
		name string
		a, b Schedule
		want bool
	}{
		{"same window", cs(1, tuesday, "", "19:00", "20:00", true), cs(2, tuesday, "", "19:00", "20:00", false), true},
		{"partial overlap", cs(1, tuesday, "", "19:00", "20:00", true), cs(2, tuesday, "", "19:30", "21:00", false), true},
		{"contained", cs(1, tuesday, "", "18:00", "22:00", false), cs(2, tuesday, "", "19:00", "20:00", true), true},
		{"touching end/start", cs(1, tuesday, "", "19:00", "20:00", true), cs(2, tuesday, "", "20:00", "21:00", true), false},
		{"different day", cs(1, tuesday, "", "19:00", "20:00", true), cs(2, []int{3}, "", "19:00", "20:00", true), false},
		{"multi day share one", cs(1, []int{1, 2, 3}, "", "19:00", "20:00", true), cs(2, []int{3, 4}, "", "19:30", "20:30", false), true},
		{"non-exclusive both allowed", cs(1, tuesday, "", "19:00", "20:00", false), cs(2, tuesday, "", "19:00", "20:00", false), false},
		{"midnight crossing vs next day early", cs(1, tuesday, "", "23:00", "01:00", true), cs(2, []int{3}, "", "00:30", "02:00", false), true},
		{"midnight crossing vs next day after end", cs(1, tuesday, "", "23:00", "01:00", true), cs(2, []int{3}, "", "01:00", "02:00", false), false},
		{"midnight crossing vs same day before", cs(1, tuesday, "", "23:00", "01:00", true), cs(2, tuesday, "", "00:30", "02:00", false), false},
		{"week wrap sat into sun", cs(1, sat, "", "23:00", "01:00", true), cs(2, sun, "", "00:30", "02:00", false), true},
		{"week wrap sun into mon", cs(1, sun, "", "23:00", "01:00", false), cs(2, []int{1}, "", "00:30", "02:00", true), true},
		{"week wrap sat into sun, miss", cs(1, sat, "", "23:00", "01:00", true), cs(2, sun, "", "01:00", "02:00", false), false},
		{"week wrap both crossing", cs(1, sat, "", "22:00", "02:00", true), cs(2, sat, "", "23:00", "03:00", true), true},
		{"instant inside window", cs(1, tuesday, "", "19:00", "20:00", true), cs(2, tuesday, "", "19:30", "", false), true},
		{"instant at window start", cs(1, tuesday, "", "19:00", "20:00", true), cs(2, tuesday, "", "19:00", "", false), true},
		{"instant at window end", cs(1, tuesday, "", "19:00", "20:00", true), cs(2, tuesday, "", "20:00", "", false), false},
		{"instant in spill", cs(1, sat, "", "23:00", "01:00", true), cs(2, sun, "", "00:10", "", false), true},
		{"instant vs instant", cs(1, tuesday, "", "19:00", "", true), cs(2, tuesday, "", "19:00", "", true), false},
		{"exclusive instant inside non-exclusive window", cs(1, tuesday, "", "19:00", "20:00", false), cs(2, tuesday, "", "19:30", "", true), true},
		{"non-exclusive instant in non-exclusive window", cs(1, tuesday, "", "19:00", "20:00", false), cs(2, tuesday, "", "19:30", "", false), false},
		{"once vs weekly same weekday", cs(1, nil, "2026-11-17", "19:30", "21:00", true), cs(2, tuesday, "", "19:00", "20:00", false), true},
		{"once vs weekly other weekday", cs(1, nil, "2026-11-18", "19:30", "21:00", true), cs(2, tuesday, "", "19:00", "20:00", false), false},
		{"once vs weekly spill from previous day", cs(1, nil, "2026-11-18", "00:30", "01:30", true), cs(2, tuesday, "", "23:00", "01:00", false), true},
		{"once vs weekly spill, after end", cs(1, nil, "2026-11-18", "01:00", "02:00", true), cs(2, tuesday, "", "23:00", "01:00", false), false},
		{"once crossing midnight vs weekly next day", cs(1, nil, "2026-11-17", "23:00", "01:00", true), cs(2, []int{3}, "", "00:30", "01:00", false), true},
		{"once vs once same date", cs(1, nil, "2026-11-17", "19:00", "20:00", true), cs(2, nil, "2026-11-17", "19:30", "20:30", false), true},
		{"once vs once different dates", cs(1, nil, "2026-11-17", "19:00", "20:00", true), cs(2, nil, "2026-11-18", "19:00", "20:00", true), false},
		{"once vs once spill next date", cs(1, nil, "2026-11-17", "23:00", "01:00", true), cs(2, nil, "2026-11-18", "00:30", "02:00", false), true},
		{"once instant vs weekly window", cs(1, nil, "2026-11-17", "19:30", "", false), cs(2, tuesday, "", "19:00", "20:00", true), true},
		{"once instant vs weekly window, later week", cs(1, nil, "2026-11-24", "19:30", "", false), cs(2, tuesday, "", "19:00", "20:00", true), true},
		{"daily vs one day", cs(1, []int{0, 1, 2, 3, 4, 5, 6}, "", "19:00", "20:00", true), cs(2, []int{5}, "", "19:59", "21:00", false), true},
	}
	for _, tt := range tests {
		for _, swap := range []bool{false, true} {
			a, b := tt.a, tt.b
			if swap {
				a, b = b, a
			}
			got := Conflicts(a, []Schedule{b})
			if (len(got) == 1) != tt.want {
				t.Errorf("%s (swap=%v): got %d conflicts, want %v", tt.name, swap, len(got), tt.want)
			}
		}
	}
}

func TestConflictsFilters(t *testing.T) {
	base := cs(1, []int{2}, "", "19:00", "20:00", true)
	other := cs(2, []int{2}, "", "19:30", "20:30", false)
	if got := Conflicts(base, []Schedule{other}); len(got) != 1 || got[0].ID != 2 {
		t.Fatalf("baseline: %v", got)
	}
	dis := other
	dis.Enabled = false
	if len(Conflicts(base, []Schedule{dis})) != 0 {
		t.Error("disabled other must not conflict")
	}
	me := base
	me.Enabled = false
	if len(Conflicts(me, []Schedule{other})) != 0 {
		t.Error("disabled subject must not conflict")
	}
	nd := other
	nd.Node = "1998"
	if len(Conflicts(base, []Schedule{nd})) != 0 {
		t.Error("different node must not conflict")
	}
	dc := other
	dc.Action, dc.End, dc.Mode = ActionDisconnect, "", ""
	if len(Conflicts(base, []Schedule{dc})) != 0 {
		t.Error("disconnect must be ignored")
	}
	if len(Conflicts(dc, []Schedule{base})) != 0 {
		t.Error("disconnect subject must be ignored")
	}
	lm := base
	lm.Mode = ModeLocalMonitor // Exclusive true but never effective
	if len(Conflicts(lm, []Schedule{other})) != 0 {
		t.Error("localmonitor is never exclusive")
	}
	if len(Conflicts(base, []Schedule{base})) != 0 {
		t.Error("a schedule must not conflict with itself")
	}
	n := base
	n.ID = 0
	if len(Conflicts(n, []Schedule{other})) != 1 {
		t.Error("new schedule must conflict")
	}
	o3 := cs(3, []int{2}, "", "19:45", "", false)
	if got := Conflicts(base, []Schedule{other, o3}); len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}

func TestConflictMessage(t *testing.T) {
	a := cs(5, []int{2}, "", "19:00", "20:30", true)
	a.Name = "Tuesday net"
	b := cs(6, nil, "2026-12-01", "19:00", "", false)
	c := cs(7, []int{1, 2, 3, 4, 5}, "", "07:00", "08:00", false)
	if m := ConflictMessage("2006", []Schedule{a}); m != `overlaps "Tuesday net" (Tue 19:00–20:30) on node 2006` {
		t.Fatal(m)
	}
	m := ConflictMessage("2006", []Schedule{a, b, c})
	if !strings.Contains(m, `"Tuesday net" (Tue 19:00–20:30), #6 (2026-12-01 19:00) and #7 (Mon–Fri 07:00–08:00) on node 2006`) {
		t.Fatal(m)
	}
}
