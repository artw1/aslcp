package dvpanel

import (
	"context"
	"sync"
	"testing"
	"time"
)

type memStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *memStore) GetSetting(k string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k], nil
}
func (s *memStore) SetSetting(k, v string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
	return nil
}

type fakeLinker struct {
	linked, known bool
	connects      []string
	exclusives    []bool
	disconnects   []string
}

func (f *fakeLinker) Connect(_ context.Context, l, b string, excl bool) error {
	f.connects = append(f.connects, l+">"+b)
	f.exclusives = append(f.exclusives, excl)
	f.linked = true
	return nil
}
func (f *fakeLinker) Disconnect(_ context.Context, l, b string, perm bool) error {
	f.disconnects = append(f.disconnects, l+">"+b)
	f.linked = false
	return nil
}
func (f *fakeLinker) Linked(l, b string) (bool, bool) { return f.linked, f.known }

func newRec(mode string) (*LinkReconciler, *fakeLinker, *memStore, *time.Time) {
	st := &memStore{m: map[string]string{SettingNode: "1997", SettingLinkMode: mode, SettingLinkNode: "1999"}}
	fl := &fakeLinker{known: true}
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	return &LinkReconciler{Store: st, Linker: fl, Now: func() time.Time { return now }}, fl, st, &now
}

func TestReconcilerReissuesMissingLink(t *testing.T) {
	r, fl, _, now := newRec(LinkPermanent)
	r.RunOnce(context.Background())
	if len(fl.connects) != 1 || fl.connects[0] != "1999>1997" {
		t.Fatalf("%v", fl.connects)
	}
	r.RunOnce(context.Background()) // linked now: nothing
	fl.linked = false
	r.RunOnce(context.Background()) // within the retry window: no hammering
	if len(fl.connects) != 1 {
		t.Fatalf("retried at once: %v", fl.connects)
	}
	*now = now.Add(time.Minute)
	r.RunOnce(context.Background())
	if len(fl.connects) != 2 {
		t.Fatalf("not retried later: %v", fl.connects)
	}
}

func TestReconcilerQuietCases(t *testing.T) {
	r, fl, st, _ := newRec(LinkOnDemand)
	r.RunOnce(context.Background())
	fl.known = false
	st.m[SettingLinkMode] = LinkPermanent
	r.RunOnce(context.Background()) // monitor does not know the node yet
	fl.known = true
	st.m[SettingLinkNode] = ""
	r.RunOnce(context.Background()) // no local node chosen
	if len(fl.connects) != 0 {
		t.Fatalf("%v", fl.connects)
	}
}

func TestReconcilerPausedByManualDisconnectAndCleared(t *testing.T) {
	r, fl, _, _ := newRec(LinkPermanent)
	ctx := context.Background()
	r.RunOnce(ctx)
	fl.linked = false
	if r.ManualDisconnect("1998", "1997") {
		t.Fatal("another local node paused it")
	}
	if r.ManualDisconnect("1999", "1996") {
		t.Fatal("another target paused it")
	}
	if !r.ManualDisconnect("1999", "1997") || !r.Paused() {
		t.Fatal("manual disconnect did not pause")
	}
	r.Retry = time.Nanosecond
	r.RunOnce(ctx)
	if len(fl.connects) != 1 {
		t.Fatalf("linked again while paused: %v", fl.connects)
	}
	// Saving clears the pause and links again
	r.Saved(ctx, LinkSettings{Mode: LinkPermanent, LocalNode: "1999", Bridge: "1997"}, LinkSettings{Mode: LinkPermanent, LocalNode: "1999", Bridge: "1997"})
	if r.Paused() {
		t.Fatal("save did not clear the pause")
	}
	r.RunOnce(ctx)
	if len(fl.connects) != 2 {
		t.Fatalf("%v", fl.connects)
	}
	// an Asterisk restart clears it too
	fl.linked = false
	r.ManualDisconnect("1999", "")
	r.AsteriskRestarted()
	if r.Paused() {
		t.Fatal("restart did not clear the pause")
	}
	r.RunOnce(ctx)
	if len(fl.connects) != 3 {
		t.Fatalf("%v", fl.connects)
	}
}

func TestReconcilerOwnCommandsAreNotManual(t *testing.T) {
	r, fl, _, _ := newRec(LinkPermanent)
	ctx := context.Background()
	r.RunOnce(ctx)
	// switching to on demand removes the permanent link with an own command
	prev := ReadLinkSettings(r.Store)
	r.Store.SetSetting(SettingLinkMode, LinkOnDemand)
	r.Saved(ctx, prev, ReadLinkSettings(r.Store))
	if len(fl.disconnects) != 1 || r.Paused() {
		t.Fatalf("disconnects %v paused %v", fl.disconnects, r.Paused())
	}
	// back to permanent: it is not paused by its own disconnect
	r.Store.SetSetting(SettingLinkMode, LinkPermanent)
	r.RunOnce(ctx)
	if len(fl.connects) != 2 {
		t.Fatalf("%v", fl.connects)
	}
	if err := r.Unlink(ctx); err != nil || len(fl.disconnects) != 2 || r.Paused() {
		t.Fatalf("%v %v %v", err, fl.disconnects, r.Paused())
	}
}

func TestReconcilerChangedNode(t *testing.T) {
	r, fl, _, _ := newRec(LinkPermanent)
	fl.linked = true
	if l := ReadLinkSettings(r.Store); l.Mode != LinkPermanent || l.LocalNode != "1999" || r.Paused() {
		t.Fatalf("%+v", l)
	}
	// moving the link to another local node removes the old one first
	prev := ReadLinkSettings(r.Store)
	cur := prev
	cur.LocalNode = "1998"
	r.Saved(context.Background(), prev, cur)
	if len(fl.disconnects) != 1 || fl.disconnects[0] != "1999>1997" {
		t.Fatalf("%v", fl.disconnects)
	}
}

func TestLinkExclusiveSettingDefaultAndRoundTrip(t *testing.T) {
	st := &memStore{m: map[string]string{}}
	if !ReadLinkSettings(st).Exclusive {
		t.Fatal("default must be on, like the analog Connect")
	}
	if err := WriteLinkSettings(st, LinkSettings{Mode: LinkOnDemand}); err != nil {
		t.Fatal(err)
	}
	if st.m[SettingLinkExclusive] != "0" || ReadLinkSettings(st).Exclusive {
		t.Fatalf("off not saved: %v", st.m)
	}
	_ = WriteLinkSettings(st, LinkSettings{Mode: LinkOnDemand, Exclusive: true})
	if st.m[SettingLinkExclusive] != "1" || !ReadLinkSettings(st).Exclusive {
		t.Fatalf("on not saved: %v", st.m)
	}
}

// Automatic relinks (start, tick, Asterisk restart) are never exclusive, even
// with the setting on; only the connect a panel save triggers is, once.
func TestReconcilerAutoRelinkNeverExclusive(t *testing.T) {
	r, fl, st, now := newRec(LinkPermanent)
	st.m[SettingLinkExclusive] = "1"
	ctx := context.Background()
	r.RunOnce(ctx) // start / tick
	fl.linked = false
	*now = now.Add(time.Minute)
	r.AsteriskRestarted() // restart (also through the panel)
	r.RunOnce(ctx)
	fl.linked = false
	*now = now.Add(time.Minute)
	r.RunOnce(ctx) // tick
	if len(fl.connects) != 3 {
		t.Fatalf("%v", fl.connects)
	}
	for i, x := range fl.exclusives {
		if x {
			t.Fatalf("automatic connect %d was exclusive", i)
		}
	}
}

func TestReconcilerSaveRelinkExclusiveOnce(t *testing.T) {
	r, fl, st, now := newRec(LinkPermanent)
	st.m[SettingLinkExclusive] = "1"
	ctx := context.Background()
	set := LinkSettings{Mode: LinkPermanent, LocalNode: "1999", Bridge: "1997", Exclusive: true}
	r.Saved(ctx, set, set)
	r.RunOnce(ctx)
	if len(fl.exclusives) != 1 || !fl.exclusives[0] {
		t.Fatalf("save relink not exclusive: %v", fl.exclusives)
	}
	fl.linked = false
	*now = now.Add(time.Minute)
	r.RunOnce(ctx) // the next one is automatic again
	if len(fl.exclusives) != 2 || fl.exclusives[1] {
		t.Fatalf("armed exclusive was not spent: %v", fl.exclusives)
	}
	// setting off: a save is not exclusive
	set.Exclusive = false
	fl.linked = false
	*now = now.Add(time.Minute)
	r.Saved(ctx, set, set)
	r.RunOnce(ctx)
	if fl.exclusives[2] {
		t.Fatalf("exclusive with the setting off: %v", fl.exclusives)
	}
	// armed, but the window passed: automatic
	set.Exclusive = true
	fl.linked = false
	*now = now.Add(time.Minute)
	r.Saved(ctx, set, set)
	*now = now.Add(ExclusiveWindow + time.Second)
	r.RunOnce(ctx)
	if fl.exclusives[3] {
		t.Fatalf("stale armed exclusive used: %v", fl.exclusives)
	}
}
