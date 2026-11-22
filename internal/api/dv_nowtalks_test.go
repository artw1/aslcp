package api

import (
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/nowcalling"
)

// feed runs ring (newest first, as dvSnapper keeps it) through the tracker.
func ncFeed(t *testing.T, tr *nowcalling.Tracker, ring []ctl.Talk) nowcalling.Snapshot {
	t.Helper()
	tr.UpdateDV(dvNowTalks(ring))
	return tr.Snapshot()
}

// After an earlier call is in the ring, a new call (after a retune) must be
// "current" with the new TG.
func TestNowTalksNewCallAfterRetune(t *testing.T) {
	now := time.Now()
	tr := nowcalling.New()
	tr.UpdateAllStar("1998", nil, true, false, false)
	old := ctl.Talk{ID: 1, Dir: "rx", Mode: ctl.ModeBM, DMRID: 2345678, TG: 91, Start: now.Add(time.Second), End: now.Add(3 * time.Second)}
	sn := ncFeed(t, tr, []ctl.Talk{old})
	if sn.Current != nil || len(sn.Recent) != 1 {
		t.Fatalf("first call: %+v", sn)
	}
	nw := ctl.Talk{ID: 2, Dir: "rx", Mode: ctl.ModeBM, DMRID: 2345678, TG: 3100, Start: now.Add(10 * time.Second)}
	sn = ncFeed(t, tr, []ctl.Talk{nw, old})
	if sn.Current == nil || sn.Current.TG != "3100" || !sn.Current.Active {
		t.Fatalf("second call not shown: %+v", sn)
	}
}

// A private reply (Parrot) shows as a private call from the parrot, not as a
// talkgroup call to our own ID.
func TestNowTalksPrivateParrotReply(t *testing.T) {
	now := time.Now()
	tr := nowcalling.New()
	tr.UpdateAllStar("1998", nil, true, false, false)
	old := ctl.Talk{ID: 1, Dir: "rx", Mode: ctl.ModeBM, DMRID: 2345678, TG: 91, Start: now.Add(time.Second), End: now.Add(3 * time.Second)}
	rep := ctl.Talk{ID: 2, Dir: "rx", Mode: ctl.ModeBM, DMRID: 9990, TG: 1234567, Private: true, Start: now.Add(10 * time.Second)}
	sn := ncFeed(t, tr, []ctl.Talk{rep, old})
	c := sn.Current
	if c == nil || !c.Private || c.TG != "" || c.Call != "Parrot" {
		t.Fatalf("private reply: %+v", c)
	}
}

// The echo test's playback shows as "Echo test" (no TG, no DMR ID) in Now
// calling and in Last heard, not as the pseudo callsign the engine sends.
func TestNowTalksEchoTest(t *testing.T) {
	now := time.Now()
	tr := nowcalling.New()
	tr.UpdateAllStar("1998", nil, true, false, false)
	old := ctl.Talk{ID: 1, Dir: "rx", Mode: ctl.ModeBM, DMRID: 2345678, TG: 91, Start: now.Add(time.Second), End: now.Add(3 * time.Second)}
	echo := ctl.Talk{ID: 2, Dir: "rx", Mode: ctl.ModeLoop, Call: ctl.EchoTestCall, Start: now.Add(10 * time.Second)}
	c := ncFeed(t, tr, []ctl.Talk{echo, old}).Current
	if c == nil || c.Call != "Echo test" || c.Network != "Echo test" || c.TG != "" || c.Private || c.DMRID != "" {
		t.Fatalf("echo test: %+v", c)
	}
	if h := dvCaller(echo); h.Call != "Echo test" || h.Dst != "" {
		t.Fatalf("heard: %+v", h)
	}
	// the operator's own transmission in LOOP keeps its callsign
	if h := dvCaller(ctl.Talk{Dir: "tx", Mode: ctl.ModeLoop, Call: "N0CALL"}); h.Call != "N0CALL" {
		t.Fatalf("tx: %+v", h)
	}
}
