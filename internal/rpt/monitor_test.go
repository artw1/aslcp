package rpt

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

type monEnv struct {
	f   *fakeNode
	ac  *ami.Client
	m   *Monitor
	sub <-chan Update
}

func startMonitor(t *testing.T, interval time.Duration, setup func(f *fakeNode)) *monEnv {
	t.Helper()
	f := newFakeNode(t, "1999")
	if setup != nil {
		f.set(setup)
	}
	ac := ami.NewClient(f.cfg())
	m := NewMonitor(ac, MonitorConfig{Nodes: []string{"1999"}, Interval: interval, PollTimeout: 2 * time.Second})
	sub, cancelSub := m.Subscribe(64)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); m.Run(ctx) }()
	t.Cleanup(func() {
		cancelSub()
		cancel()
		<-done
		ac.Close()
	})
	return &monEnv{f: f, ac: ac, m: m, sub: sub}
}

func waitFor(t *testing.T, ch <-chan Update, what string, pred func(Update) bool) Update {
	t.Helper()
	timeout := time.After(6 * time.Second)
	for {
		select {
		case u, ok := <-ch:
			if !ok {
				t.Fatalf("channel closed waiting for %s", what)
			}
			if pred(u) {
				return u
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func TestMonitorInitialPoll(t *testing.T) {
	env := startMonitor(t, time.Hour, func(f *fakeNode) {
		f.links = []string{"2000"}
		f.rx = true
	})
	// Before connecting the node is present but stale.
	u := waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })
	if u.Status.Stale || !u.Status.RxKeyed || len(u.Status.Links) != 1 || u.Status.Updated.IsZero() {
		t.Fatalf("%+v", u.Status)
	}
	st, ok := env.m.Status("1999")
	if !ok || st.Stale || st.Err != "" {
		t.Fatalf("status = %+v", st)
	}
	if _, ok := env.m.Status("nope"); ok {
		t.Error("status for unknown node")
	}
	if got := env.m.Snapshot(); len(got) != 1 {
		t.Errorf("snapshot = %v", got)
	}
	// Returned data is a copy.
	st.Links[0].Node = "mutated"
	if again, _ := env.m.Status("1999"); again.Links[0].Node == "mutated" {
		t.Error("Status shares memory")
	}
}

func TestMonitorEventDrivenKeyingWithoutPolling(t *testing.T) {
	env := startMonitor(t, time.Hour, nil)
	waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })
	before := env.f.xstatCount()

	env.f.PushEvent("RPT_RXKEYED", "Node", "1999", "Channel", "SimpleUSB/usb", "EventValue", "1",
		"LastKeyedTime", "Tue Nov 11 10:00:00 2025", "LastTxKeyedTime", "Thu Jan  1 00:00:00 1970")
	u := waitFor(t, env.sub, "rx keyed", func(u Update) bool { return u.Changes.RxKeyed })
	if !u.Status.RxKeyed {
		t.Fatal("status not keyed")
	}
	env.f.PushEvent("RPT_TXKEYED", "Node", "1999", "Channel", "x", "EventValue", "1")
	waitFor(t, env.sub, "tx keyed", func(u Update) bool { return u.Changes.TxKeyed && u.Status.TxKeyed && u.Status.RxKeyed })
	env.f.PushEvent("RPT_ETXKEYED", "Node", "1999", "Channel", "x", "EventValue", "1")
	waitFor(t, env.sub, "etx keyed", func(u Update) bool { return u.Changes.ExtTxKeyed && u.Status.ExtTxKeyed })
	env.f.PushEvent("RPT_RXKEYED", "Node", "1999", "Channel", "x", "EventValue", "0")
	u = waitFor(t, env.sub, "rx unkeyed", func(u Update) bool { return u.Changes.RxKeyed })
	if u.Status.RxKeyed {
		t.Fatal("still keyed")
	}
	// Duplicate and foreign-node events publish nothing and cause no poll.
	env.f.PushEvent("RPT_RXKEYED", "Node", "1999", "Channel", "x", "EventValue", "0")
	env.f.PushEvent("RPT_RXKEYED", "Node", "7777", "Channel", "x", "EventValue", "1")
	env.f.PushEvent("RPT_SOMETHINGNEW", "Node", "1999", "Channel", "x", "EventValue", "1")
	env.f.PushEvent("RPT_TXKEYED", "Node", "1999", "Channel", "x", "EventValue", "0") // sentinel
	u = waitFor(t, env.sub, "sentinel", func(u Update) bool { return u.Changes.TxKeyed })
	if u.Status.RxKeyed {
		t.Error("foreign node event applied")
	}
	select {
	case extra := <-env.sub:
		t.Errorf("unexpected update %+v", extra.Changes)
	case <-time.After(100 * time.Millisecond):
	}
	if n := env.f.xstatCount(); n != before {
		t.Errorf("events caused polls: %d -> %d", before, n)
	}
}

func TestMonitorPollResyncsAndSetInterval(t *testing.T) {
	env := startMonitor(t, time.Hour, nil)
	waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })
	if env.m.Interval() != time.Hour {
		t.Fatal("interval")
	}
	env.f.set(func(f *fakeNode) { f.links = []string{"2000", "2001"}; f.tx = true })
	time.Sleep(150 * time.Millisecond)
	if st, _ := env.m.Status("1999"); len(st.Links) != 0 {
		t.Fatal("polled despite 1h interval")
	}
	env.m.SetInterval(50 * time.Millisecond)
	u := waitFor(t, env.sub, "links added", func(u Update) bool { return len(u.Changes.LinksAdded) == 2 })
	if !u.Changes.TxKeyed || !u.Status.TxKeyed || !u.Changes.Network || u.Changes.Initial {
		t.Errorf("changes = %+v", u.Changes)
	}
	// Drift of connect time between polls is not a change; steady state is quiet.
	select {
	case extra := <-env.sub:
		t.Errorf("steady state published %+v", extra.Changes)
	case <-time.After(300 * time.Millisecond):
	}
	// A link keying up is noticed by the poll (SawStat).
	env.f.set(func(f *fakeNode) { f.keyed["2001"] = true })
	u = waitFor(t, env.sub, "link keyed", func(u Update) bool { return len(u.Changes.LinksChanged) == 1 })
	if u.Changes.LinksChanged[0] != "2001" {
		t.Errorf("changes = %+v", u.Changes)
	}
	env.f.set(func(f *fakeNode) { f.links = []string{"2000"}; delete(f.keyed, "2001") })
	waitFor(t, env.sub, "link removed", func(u Update) bool {
		return len(u.Changes.LinksRemoved) == 1 && u.Changes.LinksRemoved[0] == "2001"
	})
	env.m.SetInterval(1) // clamped
	if env.m.Interval() < 100*time.Millisecond {
		t.Errorf("interval = %v", env.m.Interval())
	}
}

func TestMonitorRefreshAndLinksEventTriggerPoll(t *testing.T) {
	env := startMonitor(t, time.Hour, nil)
	waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })
	env.f.set(func(f *fakeNode) { f.links = []string{"2000"} })
	env.f.PushEvent("RPT_NUMLINKS", "Node", "1999", "Channel", "x", "EventValue", "1")
	waitFor(t, env.sub, "added via event kick", func(u Update) bool { return len(u.Changes.LinksAdded) == 1 })
	env.f.set(func(f *fakeNode) { f.links = nil })
	env.m.Refresh("1999")
	waitFor(t, env.sub, "removed via Refresh", func(u Update) bool { return len(u.Changes.LinksRemoved) == 1 })
}

func TestMonitorALinksEvent(t *testing.T) {
	env := startMonitor(t, time.Hour, func(f *fakeNode) { f.links = []string{"2000", "2001"} })
	waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })
	before := env.f.xstatCount()

	// Same adjacent set, one link keyed: applied instantly, no poll.
	env.f.PushEvent("RPT_ALINKS", "Node", "1999", "Channel", "x", "EventValue", "2,2000TK,2001TU")
	u := waitFor(t, env.sub, "keyed link", func(u Update) bool { return len(u.Changes.LinksChanged) > 0 })
	if len(u.Changes.LinksChanged) != 1 || u.Changes.LinksChanged[0] != "2000" {
		t.Fatalf("changes = %+v", u.Changes)
	}
	for _, l := range u.Status.Links {
		if l.Node == "2000" && !l.Keyed {
			t.Error("2000 not keyed in status")
		}
	}
	if env.f.xstatCount() != before {
		t.Error("keyed-only ALINKS caused a poll")
	}
	// A different set of links forces a resync poll.
	env.f.set(func(f *fakeNode) { f.links = []string{"2000", "2001", "2002"} })
	env.f.PushEvent("RPT_ALINKS", "Node", "1999", "Channel", "x", "EventValue", "3,2000TU,2001TU,2002TU")
	waitFor(t, env.sub, "added via ALINKS resync", func(u Update) bool { return len(u.Changes.LinksAdded) == 1 })
	// Garbled payload also resyncs instead of corrupting state.
	env.f.set(func(f *fakeNode) { f.links = []string{"2000"} })
	env.f.PushEvent("RPT_ALINKS", "Node", "1999", "Channel", "x", "EventValue", "garbage")
	waitFor(t, env.sub, "removed after garbage", func(u Update) bool { return len(u.Changes.LinksRemoved) == 2 })
}

func TestMonitorReconnectMarksStaleThenRecovers(t *testing.T) {
	env := startMonitor(t, 100*time.Millisecond, func(f *fakeNode) { f.links = []string{"2000"} })
	waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })

	env.f.Stop()
	u := waitFor(t, env.sub, "stale", func(u Update) bool { return u.Changes.Stale })
	if !u.Status.Stale || u.Status.Err == "" || len(u.Status.Links) != 1 {
		t.Fatalf("stale update = %+v", u.Status)
	}
	if st, _ := env.m.Status("1999"); !st.Stale {
		t.Error("Status not stale while disconnected")
	}
	env.f.set(func(f *fakeNode) { f.links = []string{"2000", "2001"} })
	env.f.Restart()
	u = waitFor(t, env.sub, "recovered", func(u Update) bool { return u.Changes.Recovered })
	if u.Status.Stale || u.Status.Err != "" || len(u.Status.Links) != 2 || len(u.Changes.LinksAdded) != 1 {
		t.Fatalf("recovered update = %+v changes=%+v", u.Status, u.Changes)
	}
}

func TestMonitorPollErrorMarksStale(t *testing.T) {
	env := startMonitor(t, 50*time.Millisecond, nil)
	waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })
	env.f.set(func(f *fakeNode) { f.failNode = true })
	u := waitFor(t, env.sub, "stale", func(u Update) bool { return u.Changes.Stale })
	if u.Status.Err == "" {
		t.Error("no error text")
	}
	env.f.set(func(f *fakeNode) { f.failNode = false })
	waitFor(t, env.sub, "recovered", func(u Update) bool { return u.Changes.Recovered })
}

func TestMonitorSlowSubscriberGetsResync(t *testing.T) {
	env := startMonitor(t, time.Hour, nil)
	waitFor(t, env.sub, "initial", func(u Update) bool { return u.Changes.Initial })
	slow, cancel := env.m.Subscribe(2) // never read during the burst
	defer cancel()
	for i := 0; i < 20; i++ {
		env.f.PushEvent("RPT_RXKEYED", "Node", "1999", "Channel", "x", "EventValue", fmt.Sprint(i%2))
	}
	env.f.PushEvent("RPT_TXKEYED", "Node", "1999", "Channel", "x", "EventValue", "1") // final state: rx=1 (i=19), tx=1
	waitFor(t, env.sub, "burst done", func(u Update) bool { return u.Changes.TxKeyed })
	// The slow subscriber's queue must end with the true final state.
	var last Update
	deadline := time.After(2 * time.Second)
drain:
	for {
		select {
		case u := <-slow:
			last = u
		case <-time.After(150 * time.Millisecond):
			break drain
		case <-deadline:
			break drain
		}
	}
	if last.Node != "1999" || !last.Status.TxKeyed || !last.Status.RxKeyed {
		t.Fatalf("slow subscriber ended with %+v", last)
	}
}

func TestMonitorRunStopsAndClosesSubscribers(t *testing.T) {
	f := newFakeNode(t, "1999")
	ac := ami.NewClient(f.cfg())
	defer ac.Close()
	m := NewMonitor(ac, MonitorConfig{Nodes: []string{"1999", "1999", "2000"}, Interval: time.Hour})
	if got := m.Nodes(); len(got) != 2 {
		t.Fatalf("nodes = %v", got)
	}
	sub, cancelSub := m.Subscribe(1)
	defer cancelSub()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- m.Run(ctx) }()
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not stop")
	}
	for range sub { // closes after Run exits
	}
	late, _ := m.Subscribe(1)
	if _, ok := <-late; ok {
		t.Error("subscribe after stop not closed")
	}
}

func TestSetNodes(t *testing.T) {
	m := NewMonitor(nil, MonitorConfig{Nodes: []string{"1999", "1998"}})
	sub, cancel := m.Subscribe(1)
	defer cancel()

	if a, r := m.SetNodes([]string{"1999", "1998", "", "1999"}); len(a)+len(r) != 0 {
		t.Fatalf("unchanged set reported added=%v removed=%v", a, r)
	}
	select {
	case u := <-sub:
		t.Fatalf("unexpected update %+v", u)
	default:
	}

	a, r := m.SetNodes([]string{"1999", "1997"})
	if len(a) != 1 || a[0] != "1997" || len(r) != 1 || r[0] != "1998" {
		t.Fatalf("added=%v removed=%v", a, r)
	}
	if got := m.Nodes(); len(got) != 2 || got[0] != "1999" || got[1] != "1997" {
		t.Fatalf("nodes = %v", got)
	}
	if _, ok := m.Status("1998"); ok {
		t.Error("removed node still has a status")
	}
	st, ok := m.Status("1997")
	if !ok || !st.Stale || st.Err != "not yet polled" {
		t.Errorf("added node status = %+v ok=%v", st, ok)
	}
	if _, ok := m.Snapshot()["1998"]; ok {
		t.Error("removed node in snapshot")
	}
	if len(m.takeKicks()) != 1 || m.nodeSet["1998"] {
		t.Error("kicks/nodeSet not updated")
	}
	var gotRemoved, gotAdded bool
	for i := 0; i < 2; i++ {
		u := <-sub
		switch {
		case u.Changes.Removed && u.Node == "1998":
			gotRemoved = true
		case u.Changes.Added && u.Node == "1997":
			gotAdded = true
		}
	}
	if !gotRemoved || !gotAdded {
		t.Errorf("removed=%v added=%v", gotRemoved, gotAdded)
	}
}

func TestSetNodesConcurrent(t *testing.T) {
	env := startMonitor(t, 20*time.Millisecond, nil)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if i%2 == 0 {
				env.m.SetNodes([]string{"1999", "1998"})
			} else {
				env.m.SetNodes([]string{"1999"})
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			env.m.Nodes()
			env.m.Snapshot()
			env.m.Refresh()
			time.Sleep(time.Millisecond)
		}
	}()
	wg.Wait()
	env.m.SetNodes([]string{"1999"})
	waitFor(t, env.sub, "initial poll of 1999", func(u Update) bool { return u.Node == "1999" && u.Changes.Initial })
}
