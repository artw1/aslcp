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
	tr.UpdateAllStar("1998", nil, true, false, false)
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
	unkey := c.t
	tr.UpdateAllStar("1998", nil, true, false, false)
	if tr.Snapshot().Current == nil {
		t.Fatal("ended at the unkey, want the hangover")
	}
	c.add(dvHangover)
	tr.Tick()
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].End != unkey.Format(time.RFC3339) {
		t.Fatalf("after unkey = %+v", s)
	}
}

func ysfTalk(start time.Time) Talk {
	return Talk{Kind: KindYSF, Via: "analog_bridge", Call: "N1CALL", Start: start}
}

func TestYSFBridgeDropAndRekeyIsOneCall(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV([]Talk{ysfTalk(t0)})
	first := tr.Snapshot().Current
	if first == nil {
		t.Fatal("not shown")
	}
	c.add(10 * time.Second)
	tr.UpdateAllStar("1998", nil, true, false, false) // choppy: unkey
	c.add(2 * time.Second)
	tr.Tick()
	cur := tr.Snapshot().Current
	if cur == nil || !cur.Active || cur.ID != first.ID || cur.Start != first.Start {
		t.Fatalf("in the gap = %+v", cur)
	}
	tr.UpdateAllStar("1998", nil, true, true, false) // audio resumes
	c.add(8 * time.Second)
	tr.UpdateAllStar("1998", nil, true, true, false)
	cur = tr.Snapshot().Current
	if cur == nil || cur.ID != first.ID || !cur.Active {
		t.Fatalf("after rekey = %+v", cur)
	}
	unkey := c.t
	tr.UpdateAllStar("1998", nil, true, false, false) // final unkey
	c.add(4 * time.Second)
	tr.Tick()
	if tr.Snapshot().Current == nil {
		t.Fatal("ended inside the hangover")
	}
	c.add(1100 * time.Millisecond)
	tr.Tick()
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].ID != first.ID || s.Recent[0].End != unkey.Format(time.RFC3339) {
		t.Fatalf("after final unkey = %+v", s)
	}
	if d := s.Recent[0].end.Sub(s.Recent[0].start); d != 20*time.Second {
		t.Fatalf("duration = %v, want 20s", d)
	}
}

func TestYSFBridgeDropNeverRekeys(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV([]Talk{ysfTalk(t0)})
	c.add(3 * time.Second)
	unkey := c.t
	tr.UpdateAllStar("1998", nil, true, false, false)
	c.add(4 * time.Second)
	tr.Tick()
	if cur := tr.Snapshot().Current; cur == nil || !cur.Active {
		t.Fatalf("at +4s = %+v", cur)
	}
	c.add(1100 * time.Millisecond)
	tr.Tick()
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].End != unkey.Format(time.RFC3339) {
		t.Fatalf("at +5s = %+v", s)
	}
}

func TestYSFNewBeginTxDuringHangoverReplaces(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV([]Talk{ysfTalk(t0)})
	c.add(3 * time.Second)
	tr.UpdateAllStar("1998", nil, true, false, false)
	c.add(time.Second)
	second := ysfTalk(c.t)
	second.Call = "N2CALL"
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV([]Talk{ysfTalk(t0), second})
	s := tr.Snapshot()
	if s.Current == nil || s.Current.Call != "N2CALL" || len(s.Recent) != 1 || s.Recent[0].Call != "N1CALL" {
		t.Fatalf("snapshot = %+v", s)
	}
}

func TestDVTalkWhileUnlinkedShowsNothing(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, false, false, false) // bridge node not linked
	for _, k := range []Talk{
		{Kind: KindYSF, Via: "analog_bridge", Call: "N0CALL", Start: t0},
		talk(KindDMR, "stfu", "1234567", t0, time.Time{}),
		talk(KindDMR, "stfu", "1234567", t0, t0.Add(2*time.Second)),
	} {
		tr.UpdateDV([]Talk{k})
		c.add(10 * time.Second)
		tr.Tick()
		if s := tr.Snapshot(); s.Current != nil || len(s.Recent) != 0 {
			t.Fatalf("unlinked %+v: %+v", k, s)
		}
	}
}

func TestDVTalkWhileLinkedShows(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV([]Talk{{Kind: KindYSF, Via: "analog_bridge", Call: "N0CALL", Start: t0}})
	if cur := tr.Snapshot().Current; cur == nil || cur.Call != "N0CALL" {
		t.Fatalf("linked: %+v", cur)
	}
	c.add(time.Second)
	tr.UpdateAllStar("1998", nil, true, false, false)
	c.add(dvHangover)
	tr.UpdateAllStar("1998", nil, true, false, false)
	if s := tr.Snapshot(); s.Current != nil || len(s.Recent) != 1 {
		t.Fatalf("after unkey: %+v", s)
	}
}

func TestDVUnlinkedTalkNotShownWhenLinkComesUp(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	old := []Talk{
		talk(KindDMR, "stfu", "1234567", t0, t0.Add(2*time.Second)),
		{Kind: KindYSF, Via: "analog_bridge", Call: "N0CALL", Start: t0.Add(5 * time.Second)},
	}
	tr.UpdateDV(old)
	c.add(8 * time.Second)
	tr.UpdateAllStar("1998", nil, true, true, false) // linked now, keyed; logs re-read
	tr.UpdateDV(old)
	tr.Tick()
	if s := tr.Snapshot(); s.Current != nil || len(s.Recent) != 0 {
		t.Fatalf("retroactive: %+v", s)
	}
	// a new talk after the link does show
	c.add(time.Second)
	tr.UpdateDV(append(old, talk(KindDMR, "stfu", "1234568", c.t, time.Time{})))
	if cur := tr.Snapshot().Current; cur == nil || cur.DMRID != "1234568" {
		t.Fatalf("new talk: %+v", cur)
	}
}

func TestDVLinkDropEndsActiveCall(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tk := []Talk{{Kind: KindYSF, Via: "analog_bridge", Call: "N0CALL", Start: t0}}
	tr.UpdateDV(tk)
	if tr.Snapshot().Current == nil {
		t.Fatal("not shown")
	}
	c.add(3 * time.Second)
	tr.UpdateAllStar("1998", nil, false, false, false) // the bridge node was unlinked
	s := tr.Snapshot()
	if s.Current != nil || len(s.Recent) != 1 || s.Recent[0].End != c.t.Format(time.RFC3339) {
		t.Fatalf("after drop: %+v", s)
	}
	// the same log lines again, and a re-link, add nothing
	tr.UpdateDV(tk)
	c.add(time.Second)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV(tk)
	if s := tr.Snapshot(); s.Current != nil || len(s.Recent) != 1 {
		t.Fatalf("after relink: %+v", s)
	}
}

func TestDVStaleIsNotLinked(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, true, false)
	tr.UpdateDV([]Talk{talk(KindDMR, "stfu", "1234567", t0, time.Time{})})
	c.add(time.Second)
	tr.UpdateAllStar("1998", nil, true, true, true) // stale: state unknown
	if s := tr.Snapshot(); s.Current != nil || len(s.Recent) != 1 {
		t.Fatalf("stale: %+v", s)
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

func TestM17CallShowsReflectorAsRoom(t *testing.T) {
	c := &clk{t0}
	tr, _ := newT(c)
	tr.UpdateAllStar("1998", nil, true, false, false)
	c.add(time.Second)
	tr.UpdateDV([]Talk{{Kind: KindM17, Via: ViaEngine, Network: "M17", Call: "N0CALL", Dst: "M17-XXX C", Slot: 2, Start: t0.Add(time.Second)}})
	cur := tr.Snapshot().Current
	if cur == nil || cur.Kind != KindM17 || cur.Room != "M17-XXX C" || cur.TG != "" || cur.Slot != 0 || cur.Network != "M17" {
		t.Fatalf("current = %+v", cur)
	}
}
