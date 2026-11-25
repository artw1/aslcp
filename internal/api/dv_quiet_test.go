package api

import (
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/hostlists"
)

func dmrStatus(tg uint32, updated time.Time) ctl.Status {
	return ctl.Status{Mode: ctl.ModeDMR, Target: &ctl.Target{TG: tg}, Updated: updated}
}

func TestQuietSinceTuneCallAndReset(t *testing.T) {
	clock := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	d := &dvSnapper{now: func() time.Time { return clock }}

	d.applyLocked(ctl.Status{Mode: ctl.ModeDMR, Updated: clock}) // nothing tuned
	if d.quietKey != "" || !d.quietAt.IsZero() {
		t.Fatal("quiet time without a tuned talkgroup")
	}
	d.applyLocked(dmrStatus(759, clock)) // tune
	if d.quietKey == "" || !d.quietAt.Equal(clock) {
		t.Fatalf("tune: %q %v", d.quietKey, d.quietAt)
	}
	clock = clock.Add(10 * time.Minute)
	d.applyLocked(dmrStatus(759, d.st.Updated)) // same status again: no reset
	if !d.quietAt.Equal(clock.Add(-10 * time.Minute)) {
		t.Fatal("an unchanged status reset the quiet time")
	}
	// a received call starting and ending restarts it
	start := clock
	d.noteTalkLocked(ctl.Talk{ID: 1, Dir: "rx", Mode: ctl.ModeDMR, TG: 759, Slot: 1, Start: start})
	if !d.quietAt.Equal(start) {
		t.Fatalf("call start: %v", d.quietAt)
	}
	end := start.Add(5 * time.Second)
	d.endTalkLocked(ctl.Talk{ID: 1, Dir: "rx", Mode: ctl.ModeDMR, TG: 759, Slot: 1, Start: start, End: end}, end)
	if !d.quietAt.Equal(end) {
		t.Fatalf("call end: %v", d.quietAt)
	}
	// our own transmission counts too
	clock = end.Add(3 * time.Minute)
	d.noteTalkLocked(ctl.Talk{ID: 2, Dir: "tx", Mode: ctl.ModeDMR, TG: 759, Start: clock})
	if !d.quietAt.Equal(clock) {
		t.Fatal("tx did not restart the quiet time")
	}
	// a call of another mode does not
	before := d.quietAt
	clock = clock.Add(time.Minute)
	d.noteTalkLocked(ctl.Talk{ID: 3, Dir: "rx", Mode: ctl.ModeBM, TG: 91, Start: clock})
	if !d.quietAt.Equal(before) {
		t.Fatal("a BM call restarted the quiet time")
	}
	// retune of the same talkgroup restarts it
	clock = clock.Add(time.Minute)
	d.applyLocked(dmrStatus(759, clock))
	if !d.quietAt.Equal(clock) {
		t.Fatal("retune did not restart")
	}
	// unlink, mode change, private call: reset
	d.applyLocked(ctl.Status{Mode: ctl.ModeDMR, Updated: clock.Add(time.Second)})
	if d.quietKey != "" || !d.quietAt.IsZero() {
		t.Fatal("unlink did not reset")
	}
	d.applyLocked(dmrStatus(759, clock.Add(2*time.Second)))
	d.applyLocked(ctl.Status{Mode: ctl.ModeBM, Target: &ctl.Target{TG: 91}, Updated: clock.Add(3 * time.Second)})
	if d.quietKey != "" {
		t.Fatal("mode change did not reset")
	}
	d.applyLocked(ctl.Status{Mode: ctl.ModeDMR, Target: &ctl.Target{TG: 1234567, Private: true}, Updated: clock.Add(4 * time.Second)})
	if d.quietKey != "" {
		t.Fatal("private call counted as a tuned talkgroup")
	}
}

func TestCallerCarriesSlot(t *testing.T) {
	c := dvCaller(ctl.Talk{Mode: ctl.ModeDMR, DMRID: 2345678, TG: 759, Slot: 1, Start: time.Now()})
	if c.Slot != 1 || c.Dst != "759" {
		t.Fatalf("%+v", c)
	}
}

func TestYSFReflectorName(t *testing.T) {
	items := []hostlists.Item{{Value: "00001", Name: "US-EXAMPLE", Address: "203.0.113.5", Port: 42000}, {Value: "00002", Name: "", Address: "203.0.113.6", Port: 42001}}
	tuned := &ctl.Target{Room: "00009", Name: "TUNED-EXAMPLE", Host: "203.0.113.9", Port: 42002}
	for _, c := range []struct {
		dst  string
		tgt  *ctl.Target
		want string
	}{
		{"203.0.113.9:42002", tuned, "TUNED-EXAMPLE"},
		{"00009", tuned, "TUNED-EXAMPLE"},
		{"203.0.113.5:42000", tuned, "US-EXAMPLE"},
		{"00001", nil, "US-EXAMPLE"},
		{"203.0.113.6:42001", nil, ""},
		{"203.0.113.7:42003", tuned, ""},
	} {
		if got := ysfName(c.dst, c.tgt, items); got != c.want {
			t.Errorf("%s: got %q want %q", c.dst, got, c.want)
		}
	}
}
