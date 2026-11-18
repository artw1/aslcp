package dvswitch

import (
	"context"
	"testing"
	"time"
)

// A private call (the Parrot) is never remembered or resumed: the earlier
// talkgroup stays, and a stored private tune is dropped on load.
func TestPrivateCallIsNotRemembered(t *testing.T) {
	tr, src, _, now := newTracker(t)
	ctx := context.Background()
	boot := t0.Add(-time.Hour)
	src.info = started(dmr(t, "9", "2", ""), boot)
	tr.Poll(ctx)
	src.info = started(dmr(t, "262", "1", "262"), boot)
	*now = t0.Add(time.Minute)
	tr.Poll(ctx)
	// ABInfo holding the private form
	src.info = started(dmr(t, "9990", "1", "9990#"), boot)
	tr.Poll(ctx)
	if e := tr.Get().PerMode["DMR"]; e.Tune != "262" {
		t.Fatalf("private call replaced the tune: %+v", e)
	}
	// ABInfo holding the bare Parrot id
	src.info = started(dmr(t, "9990", "1", "9990"), boot)
	tr.Poll(ctx)
	if e := tr.Get().PerMode["DMR"]; e.Tune != "262" {
		t.Fatalf("parrot replaced the tune: %+v", e)
	}
}

func TestLoadDropsStoredPrivateTune(t *testing.T) {
	st := &fakeStore{m: map[string]string{SettingKey: `{"lastMode":"DMR","perMode":{"DMR":{"tune":"9990#","at":"x"}},"resume":true}`}}
	tr := &Tracker{Src: &fakeSrc{}, Store: st}
	if v := tr.Get(); len(v.PerMode) != 0 {
		t.Fatalf("%+v", v)
	}
}
