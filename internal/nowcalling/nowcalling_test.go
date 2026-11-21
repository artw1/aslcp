package nowcalling

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

type clk struct{ t time.Time }

func (c *clk) now() time.Time      { return c.t }
func (c *clk) add(d time.Duration) { c.t = c.t.Add(d) }
func newT(c *clk) (*Tracker, *[]Snapshot) {
	tr := New()
	tr.Now = c.now
	var got []Snapshot
	tr.OnChange = func(s Snapshot) { got = append(got, s) }
	return tr, &got
}

func TestSelectMostRecentlyKeyed(t *testing.T) {
	if Select(nil) != nil {
		t.Fatal("empty")
	}
	a := Call{ID: "a", start: t0}
	b := Call{ID: "b", start: t0.Add(2 * time.Second)}
	c := Call{ID: "c", start: t0.Add(time.Second)}
	if got := Select([]Call{a, b, c}); got.ID != "b" {
		t.Fatalf("got %s, want the most recently keyed", got.ID)
	}
	d := Call{ID: "d", start: t0}
	if got := Select([]Call{a, d}); got.ID != "d" {
		t.Fatalf("tie: %s", got.ID)
	}
}

func TestAllStarKeyUnkey(t *testing.T) {
	c := &clk{t0}
	tr, got := newT(c)
	tr.Resolve = func(x *Call) { x.Call = "N0CALL" }
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999", Since: 2 * time.Second}}, false, false, false)
	s := tr.Snapshot()
	if s.Current == nil || s.Current.Kind != KindAllStar || s.Current.Call != "N0CALL" || s.Current.Node != "1999" || s.Current.Local != "1998" {
		t.Fatalf("current = %+v", s.Current)
	}
	if want := t0.Add(-2 * time.Second).Format(time.RFC3339); s.Current.Start != want {
		t.Fatalf("start = %s, want %s (now - since key)", s.Current.Start, want)
	}
	// the same update again is no change
	n := len(*got)
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999", Since: 3 * time.Second}}, false, false, false)
	if len(*got) != n {
		t.Fatal("repeated update published")
	}
	c.add(5 * time.Second)
	tr.UpdateAllStar("1998", nil, false, false, false)
	s = tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].End == "" || s.Recent[0].Active {
		t.Fatalf("after unkey = %+v", s)
	}
}

func TestAllStarGlitchAndStale(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999"}}, false, false, false)
	c.add(200 * time.Millisecond)
	tr.UpdateAllStar("1998", nil, false, false, false)
	if len(tr.Snapshot().Recent) != 0 {
		t.Fatal("a 200 ms keyup was recorded")
	}
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999"}}, false, false, false)
	tr.UpdateAllStar("1998", nil, false, false, true) // stale: forget, no record
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 0 {
		t.Fatalf("stale = %+v", s)
	}
}

func TestOnlyOneShown(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999"}}, false, false, false)
	c.add(3 * time.Second)
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999"}, {Node: "1997"}}, false, false, false)
	if cur := tr.Snapshot().Current; cur == nil || cur.Node != "1997" {
		t.Fatalf("shown = %+v, want the newer", cur)
	}
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999"}}, false, false, false)
	if cur := tr.Snapshot().Current; cur == nil || cur.Node != "1999" {
		t.Fatalf("shown = %+v", cur)
	}
}

func talk(kind, via, id string, start time.Time, end time.Time) Talk {
	return Talk{Kind: kind, Via: via, DMRID: id, Dst: "91", Slot: 2, Start: start, End: end}
}

func TestDMRWithEndLine(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateDV([]Talk{talk(KindDMR, "stfu", "1234567", t0, time.Time{})})
	cur := tr.Snapshot().Current
	if cur == nil || cur.Kind != KindDMR || cur.Network != "BrandMeister" || cur.TG != "91" || cur.DMRID != "1234567" || !cur.Active {
		t.Fatalf("current = %+v", cur)
	}
	c.add(2500 * time.Millisecond)
	tr.UpdateDV([]Talk{talk(KindDMR, "stfu", "1234567", t0, t0.Add(2520*time.Millisecond))})
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].End != t0.Add(2520*time.Millisecond).Format(time.RFC3339) {
		t.Fatalf("after end = %+v", s)
	}
	// the same history again adds nothing
	tr.UpdateDV([]Talk{talk(KindDMR, "stfu", "1234567", t0, t0.Add(2520*time.Millisecond))})
	if len(tr.Snapshot().Recent) != 1 {
		t.Fatal("duplicate")
	}
}

func TestYSFEndsWithBridgeUnkey(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.Resolve = func(x *Call) {
		if x.Kind == KindYSF {
			x.Room = "R1"
		}
	}
	// the bridge link is keyed first, the log line follows
	tr.UpdateAllStar("1998", nil, true, true, false)
	if tr.Snapshot().Current != nil {
		t.Fatal("bridge keyed alone is no talker")
	}
	c.add(300 * time.Millisecond)
	tr.UpdateDV([]Talk{{Kind: KindYSF, Via: "analog_bridge", Call: "N1CALL", Start: t0.Add(100 * time.Millisecond)}})
	cur := tr.Snapshot().Current
	if cur == nil || cur.Kind != KindYSF || cur.Call != "N1CALL" || cur.Room != "R1" {
		t.Fatalf("current = %+v", cur)
	}
	c.add(20 * time.Second)
	tr.UpdateAllStar("1998", nil, true, true, false) // still keyed after the grace time: still talking
	if tr.Snapshot().Current == nil {
		t.Fatal("ended while the bridge link is keyed")
	}
	c.add(4 * time.Second)
	tr.UpdateAllStar("1998", nil, true, false, false)
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].End != c.t.Format(time.RFC3339) {
		t.Fatalf("after unkey = %+v", s)
	}
}

func TestDVWithoutBridgeInfoEndsAfterGrace(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateDV([]Talk{{Kind: KindYSF, Via: "analog_bridge", Call: "N1CALL", Start: t0}})
	if tr.Snapshot().Current == nil {
		t.Fatal("not shown")
	}
	c.add(10 * time.Second)
	tr.Tick()
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].End != t0.Add(noBridgeGrace).Format(time.RFC3339) {
		t.Fatalf("after grace = %+v", s)
	}
}

func TestHistoryAtStartIsRecentOnly(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	old := t0.Add(-time.Hour)
	tr.UpdateDV([]Talk{
		talk(KindDMR, "stfu", "1234567", old, old.Add(2*time.Second)),
		{Kind: KindYSF, Via: "analog_bridge", Call: "N1CALL", Start: old.Add(time.Minute)},
		talk(KindDMR, "stfu", "1234568", old.Add(2*time.Minute), old.Add(2*time.Minute+time.Second)),
	})
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 3 {
		t.Fatalf("snapshot = %+v", s)
	}
	if s.Recent[0].DMRID != "1234568" || s.Recent[1].Call != "N1CALL" || s.Recent[1].End != "" {
		t.Fatalf("order/unknown length = %+v", s.Recent)
	}
}

func TestDVAndAllStarMostRecentWins(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", []Keyed{{Node: "1999"}}, true, false, false)
	c.add(time.Second)
	tr.UpdateDV([]Talk{talk(KindDMR, "stfu", "1234567", c.t, time.Time{})})
	if cur := tr.Snapshot().Current; cur == nil || cur.Kind != KindDMR {
		t.Fatalf("current = %+v", cur)
	}
}

func TestRecentBounded(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	for i := 0; i < RecentMax+5; i++ {
		tr.UpdateAllStar("1998", []Keyed{{Node: "1999"}}, false, false, false)
		c.add(time.Second)
		tr.UpdateAllStar("1998", nil, false, false, false)
		c.add(time.Second)
	}
	if n := len(tr.Snapshot().Recent); n != RecentMax {
		t.Fatalf("recent = %d", n)
	}
}

func TestSTFULostEndLine(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV([]Talk{talk(KindDMR, "stfu", "1234567", t0, time.Time{})})
	if tr.Snapshot().Current == nil {
		t.Fatal("not shown")
	}
	c.add(10 * time.Second)
	tr.UpdateAllStar("1998", nil, true, false, false) // the bridge link went quiet, no end line came
	if s := tr.Snapshot(); s.Current != nil || len(s.Recent) != 1 {
		t.Fatalf("stuck: %+v", s)
	}
}
