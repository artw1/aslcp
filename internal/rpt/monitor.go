package rpt

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/artw1/aslcp/internal/ami"
)

// Source is what a Monitor needs from the AMI layer; *ami.Client implements
// it.
type Source interface {
	API
	Subscribe(buf int) (<-chan *ami.Message, func())
	SubscribeState(buf int) (<-chan ami.State, func())
}

// MonitorConfig configures a Monitor.
type MonitorConfig struct {
	// Nodes are the local node numbers to track.
	Nodes []string
	// Interval is the resync poll period (default 5s). Change it at runtime
	// with SetInterval, for example 1s while someone is watching.
	Interval time.Duration
	// PollTimeout bounds one node poll (default 10s).
	PollTimeout time.Duration
}

// Changes describes what an Update changed.
type Changes struct {
	// Resync marks an update re-sent after a slow subscriber overflowed;
	// treat Status as authoritative and ignore the other fields.
	Resync bool
	// Initial is the first successful poll of the node.
	Initial bool
	// Added: the node joined the monitored set (SetNodes). Removed: it left
	// it; Status is its last known state and no further updates follow.
	// Consumers should re-read the whole node list for either.
	Added   bool
	Removed bool
	// Stale: the node just became stale. Recovered: it just stopped being so.
	Stale     bool
	Recovered bool

	RxKeyed    bool
	TxKeyed    bool
	ExtTxKeyed bool
	Network    bool
	Flags      bool

	LinksAdded   []string
	LinksRemoved []string
	LinksChanged []string // mode, state, keyed, address or direction changed
}

func (c Changes) any() bool {
	return c.Initial || c.Added || c.Removed || c.Stale || c.Recovered || c.RxKeyed || c.TxKeyed || c.ExtTxKeyed ||
		c.Network || c.Flags || len(c.LinksAdded) > 0 || len(c.LinksRemoved) > 0 || len(c.LinksChanged) > 0
}

// Update is published to subscribers whenever a node's state changes. Status
// is a full, independent copy.
type Update struct {
	Node    string
	Status  NodeStatus
	Changes Changes
}

type sub struct{ ch chan Update }

// Monitor keeps an in-memory NodeStatus per node current. Manager events
// give instant keying and link-key changes; a periodic poll of
// XStat+SawStat resyncs everything else. While AMI is disconnected nodes
// are marked stale. All methods are safe for concurrent use.
type Monitor struct {
	src     Source
	rpt     *Client
	nodes   []string
	nodeSet map[string]bool
	timeout time.Duration

	interval   atomic.Int64
	intervalCh chan struct{}
	kickCh     chan struct{}

	mu         sync.Mutex
	status     map[string]*NodeStatus
	kicks      map[string]bool
	lastEvent  map[string]time.Time
	connected  bool
	subs       map[*sub]struct{}
	subsClosed bool
}

// NewMonitor creates a Monitor. Call Run to start it.
func NewMonitor(src Source, cfg MonitorConfig) *Monitor {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.PollTimeout <= 0 {
		cfg.PollTimeout = 10 * time.Second
	}
	m := &Monitor{
		src:        src,
		rpt:        New(src),
		timeout:    cfg.PollTimeout,
		nodeSet:    map[string]bool{},
		intervalCh: make(chan struct{}, 1),
		kickCh:     make(chan struct{}, 1),
		status:     map[string]*NodeStatus{},
		kicks:      map[string]bool{},
		lastEvent:  map[string]time.Time{},
		subs:       map[*sub]struct{}{},
	}
	for _, n := range cfg.Nodes {
		if !m.nodeSet[n] {
			m.nodeSet[n] = true
			m.nodes = append(m.nodes, n)
			m.status[n] = &NodeStatus{Node: n, Stale: true, Err: "not yet polled"}
		}
	}
	m.interval.Store(int64(cfg.Interval))
	return m
}

// Interval returns the current poll interval.
func (m *Monitor) Interval() time.Duration { return time.Duration(m.interval.Load()) }

// SetInterval changes the poll period (minimum 100ms) and takes effect
// immediately.
func (m *Monitor) SetInterval(d time.Duration) {
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	m.interval.Store(int64(d))
	select {
	case m.intervalCh <- struct{}{}:
	default:
	}
}

// Nodes returns the monitored node numbers.
func (m *Monitor) Nodes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.nodes...)
}

// SetNodes replaces the monitored node set. New nodes start stale ("not yet
// polled") and are polled at once; dropped nodes are forgotten and
// subscribers get an Update with Changes.Removed. Duplicates and empty
// entries are ignored. It returns the nodes added and removed (both empty
// when the set is unchanged).
func (m *Monitor) SetNodes(nodes []string) (added, removed []string) {
	want := make([]string, 0, len(nodes))
	wantSet := map[string]bool{}
	for _, n := range nodes {
		if n != "" && !wantSet[n] {
			wantSet[n] = true
			want = append(want, n)
		}
	}
	m.mu.Lock()
	for _, n := range m.nodes {
		if !wantSet[n] {
			removed = append(removed, n)
		}
	}
	for _, n := range want {
		if !m.nodeSet[n] {
			added = append(added, n)
		}
	}
	if len(added) == 0 && len(removed) == 0 {
		m.mu.Unlock()
		return nil, nil
	}
	for _, n := range removed {
		last := NodeStatus{Node: n}
		if s := m.status[n]; s != nil {
			last = s.Clone()
		}
		delete(m.nodeSet, n)
		delete(m.status, n)
		delete(m.kicks, n)
		delete(m.lastEvent, n)
		m.nodes = removeString(m.nodes, n)
		m.publish(Update{Node: n, Status: last, Changes: Changes{Removed: true}})
	}
	for _, n := range added {
		st := &NodeStatus{Node: n, Stale: true, Err: "not yet polled"}
		m.nodeSet[n] = true
		m.nodes = append(m.nodes, n)
		m.status[n] = st
		m.publish(Update{Node: n, Status: st.Clone(), Changes: Changes{Added: true}})
	}
	m.mu.Unlock()
	if len(added) > 0 {
		m.Refresh(added...)
	}
	return added, removed
}

func removeString(l []string, v string) []string {
	out := make([]string, 0, len(l))
	for _, x := range l {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// Status returns a copy of one node's current status.
func (m *Monitor) Status(node string) (NodeStatus, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.status[node]
	if !ok {
		return NodeStatus{}, false
	}
	return s.Clone(), true
}

// Snapshot returns copies of all node statuses.
func (m *Monitor) Snapshot() map[string]NodeStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]NodeStatus, len(m.status))
	for k, v := range m.status {
		out[k] = v.Clone()
	}
	return out
}

// Subscribe returns a channel of updates and a cancel func. The buffer is at
// least len(nodes)+1. The monitor never blocks on a slow subscriber: if its
// buffer overflows, the queue is replaced by one Resync update per node
// carrying the current status.
func (m *Monitor) Subscribe(buf int) (<-chan Update, func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Room for a full resync even if the node set later grows a little.
	if min := 2*len(m.nodes) + 2; buf < min {
		buf = min
	}
	s := &sub{ch: make(chan Update, buf)}
	if m.subsClosed {
		close(s.ch)
		return s.ch, func() {}
	}
	m.subs[s] = struct{}{}
	var once sync.Once
	return s.ch, func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if _, ok := m.subs[s]; ok {
				delete(m.subs, s)
				close(s.ch)
			}
		})
	}
}

// publish must be called with m.mu held.
func (m *Monitor) publish(u Update) {
	for s := range m.subs {
		select {
		case s.ch <- u:
			continue
		default:
		}
		for drained := false; !drained; {
			select {
			case <-s.ch:
			default:
				drained = true
			}
		}
		for _, n := range m.nodes {
			select {
			case s.ch <- Update{Node: n, Status: m.status[n].Clone(), Changes: Changes{Resync: true}}:
			default:
			}
		}
	}
}

// Refresh asks for an immediate poll of the given nodes (all if none given).
func (m *Monitor) Refresh(nodes ...string) {
	m.mu.Lock()
	if len(nodes) == 0 {
		nodes = m.nodes
	}
	for _, n := range nodes {
		if m.nodeSet[n] {
			m.kicks[n] = true
		}
	}
	m.mu.Unlock()
	select {
	case m.kickCh <- struct{}{}:
	default:
	}
}

func (m *Monitor) takeKicks() map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := m.kicks
	m.kicks = map[string]bool{}
	return k
}

// Run drives the monitor until ctx is done or the source closes. Events and
// connection state are handled on this goroutine; polling runs on another so
// slow polls never delay event handling.
func (m *Monitor) Run(ctx context.Context) error {
	events, cancelE := m.src.Subscribe(1024)
	defer cancelE()
	states, cancelS := m.src.SubscribeState(16)
	defer cancelS()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); m.pollLoop(ctx) }()
	defer func() {
		wg.Wait()
		m.mu.Lock()
		m.subsClosed = true
		for s := range m.subs {
			close(s.ch)
			delete(m.subs, s)
		}
		m.mu.Unlock()
	}()

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-events:
			if !ok {
				return nil
			}
			m.handleEvent(msg)
		case st, ok := <-states:
			if !ok {
				return nil
			}
			m.handleState(st)
		}
	}
}

func (m *Monitor) handleState(st ami.State) {
	m.mu.Lock()
	m.connected = st.Connected
	m.mu.Unlock()
	if st.Connected {
		m.Refresh()
		return
	}
	reason := "AMI disconnected"
	if st.Err != nil {
		reason += ": " + st.Err.Error()
	}
	for _, n := range m.Nodes() {
		m.markStale(n, reason)
	}
}

func (m *Monitor) isConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *Monitor) pollLoop(ctx context.Context) {
	timer := time.NewTimer(m.Interval())
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			m.poll(ctx, nil)
		case <-m.kickCh:
			m.poll(ctx, m.takeKicks())
		case <-m.intervalCh:
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(m.Interval())
	}
}

// poll refreshes the given nodes (all when only is nil).
func (m *Monitor) poll(ctx context.Context, only map[string]bool) {
	if !m.isConnected() {
		return
	}
	for _, n := range m.Nodes() {
		if only != nil && !only[n] {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		started := time.Now()
		pctx, cancel := context.WithTimeout(ctx, m.timeout)
		st, err := m.rpt.NodeStatus(pctx, n)
		cancel()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, ami.ErrClosed) || errors.Is(err, ami.ErrNotConnected) {
				continue // state handling marks nodes stale on disconnect
			}
			m.markStale(n, err.Error())
			continue
		}
		raced := m.apply(n, st, started)
		if raced {
			m.Refresh(n) // a keying event arrived mid-poll; the poll may predate it
		}
	}
}

func (m *Monitor) markStale(node, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status[node]
	if s == nil {
		return
	}
	was := s.Stale
	s.Stale, s.Err = true, reason
	if !was {
		m.publish(Update{Node: node, Status: s.Clone(), Changes: Changes{Stale: true}})
	}
}

// apply stores a fresh poll result and publishes the diff. It reports
// whether a keying event for the node arrived after started.
func (m *Monitor) apply(node string, fresh NodeStatus, started time.Time) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.nodeSet[node] {
		return false // removed while the poll ran
	}
	old := m.status[node]
	fresh.Stale, fresh.Err = false, ""
	ch := diffStatus(old, &fresh)
	m.status[node] = &fresh
	if ch.any() {
		m.publish(Update{Node: node, Status: fresh.Clone(), Changes: ch})
	}
	return m.lastEvent[node].After(started)
}

const driftTolerance = 2 * time.Second

// sameInstant compares "age" values (durations measured at updated times)
// by the instant they imply, so a steadily growing age is not a change.
func sameAge(aAt time.Time, a *time.Duration, bAt time.Time, b *time.Duration) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	d := aAt.Add(-*a).Sub(bAt.Add(-*b))
	if d < 0 {
		d = -d
	}
	return d <= driftTolerance
}

func linkChanged(a Link, aAt time.Time, b Link, bAt time.Time) bool {
	if a.Mode != b.Mode || a.State != b.State || a.Keyed != b.Keyed || a.IP != b.IP ||
		a.Direction != b.Direction || a.Reconnects != b.Reconnects {
		return true
	}
	ac, bc := a.Connected, b.Connected
	if !sameAge(aAt, &ac, bAt, &bc) {
		return true
	}
	return !sameAge(aAt, a.SinceKey, bAt, b.SinceKey) || !sameAge(aAt, a.SinceUnkey, bAt, b.SinceUnkey)
}

func diffStatus(old, fresh *NodeStatus) Changes {
	var c Changes
	if old == nil || old.Updated.IsZero() {
		c.Initial = true
		return c
	}
	c.Recovered = old.Stale
	c.RxKeyed = old.RxKeyed != fresh.RxKeyed
	c.TxKeyed = old.TxKeyed != fresh.TxKeyed
	c.ExtTxKeyed = old.ExtTxKeyed != fresh.ExtTxKeyed
	c.Flags = old.Flags != fresh.Flags
	if len(old.Network) != len(fresh.Network) {
		c.Network = true
	} else {
		for i := range old.Network {
			if old.Network[i] != fresh.Network[i] {
				c.Network = true
				break
			}
		}
	}
	oldBy := map[string]Link{}
	for _, l := range old.Links {
		oldBy[l.Node] = l
	}
	newBy := map[string]bool{}
	for _, l := range fresh.Links {
		newBy[l.Node] = true
		o, ok := oldBy[l.Node]
		switch {
		case !ok:
			c.LinksAdded = append(c.LinksAdded, l.Node)
		case linkChanged(o, old.Updated, l, fresh.Updated):
			c.LinksChanged = append(c.LinksChanged, l.Node)
		}
	}
	for _, l := range old.Links {
		if !newBy[l.Node] {
			c.LinksRemoved = append(c.LinksRemoved, l.Node)
		}
	}
	sort.Strings(c.LinksAdded)
	sort.Strings(c.LinksRemoved)
	sort.Strings(c.LinksChanged)
	return c
}

func (m *Monitor) handleEvent(msg *ami.Message) {
	ev, ok := ParseEvent(msg)
	if !ok {
		return
	}
	m.mu.Lock()
	known := m.nodeSet[ev.Node]
	m.mu.Unlock()
	if !known {
		return
	}
	switch ev.Kind {
	case EventRxKeyed, EventTxKeyed, EventExtTxKeyed:
		m.applyKeying(ev)
	case EventALinks:
		m.applyALinks(ev)
	case EventLinks, EventNumLinks, EventNumALinks:
		m.Refresh(ev.Node)
	}
}

func (m *Monitor) applyKeying(ev Event) {
	m.mu.Lock()
	m.lastEvent[ev.Node] = time.Now()
	s := m.status[ev.Node]
	if s == nil || s.Updated.IsZero() {
		m.mu.Unlock()
		m.Refresh(ev.Node)
		return
	}
	var ch Changes
	switch ev.Kind {
	case EventRxKeyed:
		ch.RxKeyed, s.RxKeyed = s.RxKeyed != ev.Keyed, ev.Keyed
	case EventTxKeyed:
		ch.TxKeyed, s.TxKeyed = s.TxKeyed != ev.Keyed, ev.Keyed
	case EventExtTxKeyed:
		ch.ExtTxKeyed, s.ExtTxKeyed = s.ExtTxKeyed != ev.Keyed, ev.Keyed
	}
	if ch.any() {
		m.publish(Update{Node: ev.Node, Status: s.Clone(), Changes: ch})
	}
	m.mu.Unlock()
}

// applyALinks applies the per-link keyed flags from RPT_ALINKS. If the set
// of adjacent links or any link mode differs from what is known, the node is
// re-polled instead, since a poll also carries the connection details.
func (m *Monitor) applyALinks(ev Event) {
	m.mu.Lock()
	m.lastEvent[ev.Node] = time.Now()
	s := m.status[ev.Node]
	if s == nil || s.Updated.IsZero() || ev.Err != nil {
		m.mu.Unlock()
		m.Refresh(ev.Node)
		return
	}
	adj := map[string]int{} // node -> index in s.Links (local monitors excluded)
	for i, l := range s.Links {
		if l.Mode != ModeLocalMonitor {
			adj[l.Node] = i
		}
	}
	resync := len(adj) != len(ev.ALinks)
	for _, a := range ev.ALinks {
		i, ok := adj[a.Node]
		if !ok || s.Links[i].Mode != a.Mode {
			resync = true
			break
		}
	}
	if resync {
		m.mu.Unlock()
		m.Refresh(ev.Node)
		return
	}
	var changed []string
	for _, a := range ev.ALinks {
		l := &s.Links[adj[a.Node]]
		if l.Keyed != a.Keyed {
			l.Keyed = a.Keyed
			changed = append(changed, a.Node)
		}
	}
	if len(changed) > 0 {
		sort.Strings(changed)
		m.publish(Update{Node: ev.Node, Status: s.Clone(), Changes: Changes{LinksChanged: changed}})
	}
	m.mu.Unlock()
}
