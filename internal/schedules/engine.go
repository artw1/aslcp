package schedules

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Settings keys (see docs/SCHEDULES.md).
const (
	KeyEnabled      = "schedules.enabled"
	KeyPausedReason = "schedules.paused_reason"
	KeyPausedAt     = "schedules.paused_at"
	KeyState        = "schedules.state"
)

// RetryEvery is the delay between attempts after an error; RetrySingle how
// long after its time a single event (or a window's closing disconnect) is
// retried.
const (
	RetryEvery  = time.Minute
	RetrySingle = 10 * time.Minute
)

// Store is the persistence the engine uses (store.Store implements it).
// GetSetting errors mean "not set".
type Store interface {
	ListSchedules() ([]Schedule, error)
	SetScheduleResult(id int64, at time.Time, result string) error
	SetScheduleEnabled(id int64, enabled bool) error
	GetSetting(k string) (string, error)
	SetSetting(k, v string) error
}

// Linker sends link commands to the node. LinkMode reports the target's
// state from the monitor's latest status: mode is "transceive", "monitor",
// "localmonitor" or "connecting"; known is false when the node is not a
// current local node (or has no status yet).
type Linker interface {
	Connect(ctx context.Context, node, target, mode string, permanent, exclusive bool) error
	Disconnect(ctx context.Context, node, target string, permanent bool) error
	LinkMode(node, target string) (mode string, linked bool, known bool)
}

// Engine executes schedules. Create it with a struct literal (Store and
// Linker are required) and call Run.
type Engine struct {
	Store  Store
	Linker Linker
	Now    func() time.Time
	Loc    *time.Location
	Tick   time.Duration
	Logger interface{ Printf(string, ...any) }
	// OnChange is called (without locks held) after the visible state
	// changed.
	OnChange func()
	// Zone overrides the detected time zone name.
	Zone string
	// StartDelay postpones the first tick of Run (the monitor needs a moment
	// to have a status).
	StartDelay time.Duration
	// CmdTimeout bounds one link command (default 30 s).
	CmdTimeout time.Duration

	runMu    sync.Mutex // serialises RunOnce
	mu       sync.Mutex
	inited   bool
	enabled  bool
	reason   string
	pausedAt time.Time
	state    []Occ
	lastTick time.Time
	pending  map[pkey]time.Time // single events being handled -> event time
	tries    map[pkey]*try
	gen      uint64
	wake     chan struct{}
	zone     string
}

type pkey struct {
	kind  byte // 'c' connect (window or single), 'd' disconnect single, 'e' window end
	id    int64
	start int64
}

type try struct{ first, next time.Time }

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) loc() *time.Location {
	if e.Loc != nil {
		return e.Loc
	}
	return time.Local
}

func (e *Engine) logf(f string, a ...any) {
	if e.Logger != nil {
		e.Logger.Printf(f, a...)
	}
}

func (e *Engine) notify() {
	if e.OnChange != nil {
		e.OnChange()
	}
}

// init loads the persisted state; the caller holds mu.
func (e *Engine) init() {
	if e.inited {
		return
	}
	e.inited = true
	e.wake = make(chan struct{}, 1)
	e.pending = map[pkey]time.Time{}
	e.tries = map[pkey]*try{}
	e.enabled = true
	if v, err := e.Store.GetSetting(KeyEnabled); err == nil && v == "false" {
		e.enabled = false
	}
	if v, err := e.Store.GetSetting(KeyPausedReason); err == nil {
		e.reason = v
	}
	if v, err := e.Store.GetSetting(KeyPausedAt); err == nil && v != "" {
		if t, err := time.Parse(time.RFC3339, v); err == nil {
			e.pausedAt = t
		}
	}
	if v, err := e.Store.GetSetting(KeyState); err == nil && v != "" {
		if err := json.Unmarshal([]byte(v), &e.state); err != nil {
			e.logf("schedules: ignoring unreadable state: %v", err)
			e.state = nil
		}
	}
	e.lastTick = e.now()
	if e.Zone != "" {
		e.zone = e.Zone
	} else {
		e.zone = ZoneName(SystemZoneSource(), e.loc(), e.now())
	}
}

func (e *Engine) saveState() {
	b, _ := json.Marshal(e.state)
	if len(e.state) == 0 {
		b = []byte("[]")
	}
	if err := e.Store.SetSetting(KeyState, string(b)); err != nil {
		e.logf("schedules: saving state: %v", err)
	}
}

func (e *Engine) saveSwitch() {
	v := "true"
	if !e.enabled {
		v = "false"
	}
	at := ""
	if !e.pausedAt.IsZero() {
		at = e.pausedAt.Format(time.RFC3339)
	}
	for k, val := range map[string]string{KeyEnabled: v, KeyPausedReason: e.reason, KeyPausedAt: at} {
		if err := e.Store.SetSetting(k, val); err != nil {
			e.logf("schedules: saving %s: %v", k, err)
		}
	}
}

func (e *Engine) findOcc(id, start int64) int {
	for i, o := range e.state {
		if o.ID == id && o.Start.Unix() == start {
			return i
		}
	}
	return -1
}

func (e *Engine) dropOcc(i int) { e.state = append(e.state[:i], e.state[i+1:]...) }

func (e *Engine) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

// Enabled reports the global switch.
func (e *Engine) Enabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.init()
	return e.enabled
}

// SetEnabled flips the global switch (an operator action). Switching on
// clears the pause reason; a window running now is caught up on the next
// tick, but cancelled occurrences and single events are not replayed.
func (e *Engine) SetEnabled(on bool) {
	e.mu.Lock()
	e.init()
	e.enabled = on
	e.reason, e.pausedAt = "", time.Time{}
	e.saveSwitch()
	e.pending = map[pkey]time.Time{}
	e.tries = map[pkey]*try{}
	e.lastTick = e.now()
	e.gen++
	e.mu.Unlock()
	e.kick()
	e.notify()
}

// Pause is the effect of a manual link action: occurrences running now are
// cancelled (they stay cancelled when schedules are re-enabled) and the
// global switch goes off with the reason. An existing pause keeps its reason.
func (e *Engine) Pause(reason string) {
	e.mu.Lock()
	e.init()
	now := e.now()
	for i := range e.state {
		if e.state[i].Status == StatusConnected {
			e.state[i].Status = StatusCancelled
		}
	}
	if list, err := e.Store.ListSchedules(); err == nil {
		for _, s := range list {
			if !s.Enabled || !s.isWindow() {
				continue
			}
			for _, o := range occurrences(s, now.Add(-48*time.Hour), now, e.loc()) {
				if o.end.After(now) && e.findOcc(s.ID, o.start.Unix()) < 0 {
					e.state = append(e.state, Occ{ID: s.ID, Start: o.start, End: o.end, Status: StatusCancelled})
				}
			}
		}
	} else {
		e.logf("schedules: pause: %v", err)
	}
	e.saveState()
	if e.enabled {
		e.enabled, e.reason, e.pausedAt = false, reason, now.Truncate(time.Second)
		e.saveSwitch()
	}
	e.pending = map[pkey]time.Time{}
	e.tries = map[pkey]*try{}
	e.gen++
	e.mu.Unlock()
	e.notify()
}

// Changed forgets the running occurrence and pending retries of schedule id
// (after it was saved, enabled, disabled or deleted). The link is not
// touched; a window running now is caught up on the next tick.
func (e *Engine) Changed(id int64) {
	e.mu.Lock()
	e.init()
	kept := e.state[:0]
	for _, o := range e.state {
		if o.ID != id {
			kept = append(kept, o)
		}
	}
	if len(kept) != len(e.state) {
		e.state = kept
		e.saveState()
	}
	for k := range e.pending {
		if k.id == id {
			delete(e.pending, k)
		}
	}
	for k := range e.tries {
		if k.id == id {
			delete(e.tries, k)
		}
	}
	e.gen++
	e.mu.Unlock()
	e.kick()
	e.notify()
}

// Run processes events until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	e.mu.Lock()
	e.init()
	e.mu.Unlock()
	if e.StartDelay > 0 {
		select {
		case <-ctx.Done():
			return
		case <-time.After(e.StartDelay):
		}
	}
	tick := e.Tick
	if tick <= 0 {
		tick = 15 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		e.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.wake:
		}
	}
}

type item struct {
	key  pkey
	s    Schedule
	at   time.Time // event (or window start) time
	end  time.Time // window end, for connect windows and ends
	rank int
}

// RunOnce does one tick: it executes what is due now. Exported for tests.
func (e *Engine) RunOnce(ctx context.Context) {
	e.runMu.Lock()
	defer e.runMu.Unlock()
	e.mu.Lock()
	e.init()
	now := e.now()
	work, changed := e.plan(now)
	gen := e.gen
	e.mu.Unlock()
	if changed {
		e.notify()
	}
	for _, it := range work {
		e.mu.Lock()
		stale := e.gen != gen
		e.mu.Unlock()
		if stale {
			return
		}
		res, ok := e.exec(ctx, it)
		e.mu.Lock()
		if e.gen != gen {
			e.mu.Unlock()
			return
		}
		e.record(it, res, ok)
		e.mu.Unlock()
		e.notify()
	}
}

// plan collects the due work; the caller holds mu.
func (e *Engine) plan(now time.Time) (work []item, changed bool) {
	from := e.lastTick
	e.lastTick = now
	loc := e.loc()
	if !e.enabled {
		kept := e.state[:0]
		for _, o := range e.state {
			if o.End.Add(RetrySingle).After(now) {
				kept = append(kept, o)
			}
		}
		if len(kept) != len(e.state) {
			e.state = kept
			e.saveState()
		}
		return nil, false
	}
	list, err := e.Store.ListSchedules()
	if err != nil {
		e.logf("schedules: %v", err)
		return nil, false
	}
	byID := map[int64]Schedule{}
	for _, s := range list {
		byID[s.ID] = s
	}
	// Drop occurrences of schedules that are gone or switched off.
	kept := e.state[:0]
	for _, o := range e.state {
		if s, ok := byID[o.ID]; ok && s.Enabled {
			if o.Status == StatusCancelled && !o.End.After(now) {
				continue
			}
			kept = append(kept, o)
		}
	}
	if len(kept) != len(e.state) {
		e.state = kept
		e.saveState()
	}
	due := func(k pkey) bool {
		t := e.tries[k]
		return t == nil || !now.Before(t.next)
	}
	// Single events whose time passed since the last tick.
	for _, s := range list {
		if !s.Enabled || s.isWindow() {
			continue
		}
		for _, o := range occurrences(s, from, now, loc) {
			if !o.start.After(from) || now.Sub(o.start) > RetrySingle {
				continue
			}
			k := pkey{'c', s.ID, o.start.Unix()}
			if s.Action == ActionDisconnect {
				k.kind = 'd'
			}
			if _, ok := e.pending[k]; !ok {
				e.pending[k] = o.start
			}
		}
	}
	for k, at := range e.pending {
		s, ok := byID[k.id]
		if !ok || !s.Enabled || now.Sub(at) > RetrySingle {
			delete(e.pending, k)
			delete(e.tries, k)
			continue
		}
		if !due(k) {
			continue
		}
		rank := rankConnect
		if k.kind == 'd' {
			rank = rankDisconnect
		}
		work = append(work, item{key: k, s: s, at: at, rank: rank})
	}
	// Window ends (also those missed while the panel was down).
	for _, o := range e.state {
		if o.Status != StatusConnected || o.End.After(now) {
			continue
		}
		k := pkey{'e', o.ID, o.Start.Unix()}
		if t := e.tries[k]; t != nil && now.Sub(t.first) > RetrySingle {
			delete(e.tries, k)
			if i := e.findOcc(o.ID, o.Start.Unix()); i >= 0 {
				e.dropOcc(i)
				e.saveState()
			}
			continue
		}
		if due(k) {
			work = append(work, item{key: k, s: byID[o.ID], at: o.End, end: o.End, rank: rankEnd})
		}
	}
	// Windows running now that nothing handled yet: the start, retries and
	// the catch-up are the same thing.
	for _, s := range list {
		if !s.Enabled || !s.isWindow() {
			continue
		}
		for _, o := range occurrences(s, now.Add(-48*time.Hour), now, loc) {
			if !o.end.After(now) || e.findOcc(s.ID, o.start.Unix()) >= 0 {
				continue
			}
			k := pkey{'c', s.ID, o.start.Unix()}
			if due(k) {
				work = append(work, item{key: k, s: s, at: o.start, end: o.end, rank: rankConnect})
			}
		}
	}
	sort.SliceStable(work, func(i, j int) bool {
		a, b := work[i], work[j]
		am, bm := a.at.Truncate(time.Minute), b.at.Truncate(time.Minute)
		if !am.Equal(bm) {
			return am.Before(bm)
		}
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		return a.s.ID < b.s.ID
	})
	for k, t := range e.tries {
		if now.Sub(t.first) > 3*24*time.Hour {
			delete(e.tries, k)
		}
	}
	return work, e.finishOnce(list, now)
}

// finishOnce disables one-off schedules whose last event is over and has no
// work left; the caller holds mu.
func (e *Engine) finishOnce(list []Schedule, now time.Time) bool {
	changed := false
	for _, s := range list {
		if !s.Enabled || s.Date == "" {
			continue
		}
		d, err := time.ParseInLocation("2006-01-02", s.Date, e.loc())
		if err != nil {
			continue
		}
		occ := occurrences(s, d.AddDate(0, 0, -1), d.AddDate(0, 0, 2), e.loc())
		if len(occ) == 0 {
			continue
		}
		last := occ[len(occ)-1]
		lastEv := last.start
		if !last.end.IsZero() {
			lastEv = last.end
		}
		if lastEv.After(now) {
			continue
		}
		busy := false
		for k := range e.pending {
			if k.id == s.ID {
				busy = true
			}
		}
		for _, o := range e.state {
			if o.ID == s.ID {
				busy = true
			}
		}
		if busy {
			continue
		}
		if err := e.Store.SetScheduleEnabled(s.ID, false); err != nil {
			e.logf("schedules: disabling one-off %d: %v", s.ID, err)
			continue
		}
		changed = true
	}
	return changed
}

func (e *Engine) cmdCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	d := e.CmdTimeout
	if d <= 0 {
		d = 30 * time.Second
	}
	return context.WithTimeout(ctx, d)
}

// exec runs one item; ok is false for an error (to retry).
func (e *Engine) exec(ctx context.Context, it item) (string, bool) {
	s := it.s
	if it.key.kind == 'c' {
		mode, linked, known := e.Linker.LinkMode(s.Node, s.Target)
		if !known {
			return "error: node not available", false
		}
		if linked && (mode == s.Mode || mode == "connecting") {
			return "skipped: already connected", true
		}
		c, cancel := e.cmdCtx(ctx)
		defer cancel()
		if err := e.Linker.Connect(c, s.Node, s.Target, s.Mode, s.Permanent, s.Exclusive); err != nil {
			e.logf("schedule %d: connect %s -> %s: %v", s.ID, s.Node, s.Target, err)
			return "error: " + err.Error(), false
		}
		e.logf("schedule %d: connected %s -> %s (%s)", s.ID, s.Node, s.Target, s.Mode)
		return "ok", true
	}
	_, linked, known := e.Linker.LinkMode(s.Node, s.Target)
	if !known {
		return "error: node not available", false
	}
	if !linked {
		return "skipped: not connected", true
	}
	c, cancel := e.cmdCtx(ctx)
	defer cancel()
	if err := e.Linker.Disconnect(c, s.Node, s.Target, s.Permanent); err != nil {
		e.logf("schedule %d: disconnect %s -> %s: %v", s.ID, s.Node, s.Target, err)
		return "error: " + err.Error(), false
	}
	e.logf("schedule %d: disconnected %s -> %s", s.ID, s.Node, s.Target)
	return "ok", true
}

// record stores an item's outcome; the caller holds mu.
func (e *Engine) record(it item, result string, ok bool) {
	now := e.now()
	if err := e.Store.SetScheduleResult(it.s.ID, now, result); err != nil {
		e.logf("schedules: saving result: %v", err)
	}
	k := it.key
	if !ok {
		t := e.tries[k]
		if t == nil {
			t = &try{first: now}
			e.tries[k] = t
		}
		t.next = now.Add(RetryEvery)
		return
	}
	delete(e.tries, k)
	switch k.kind {
	case 'c':
		if !it.end.IsZero() {
			e.state = append(e.state, Occ{ID: k.id, Start: it.at, End: it.end, Status: StatusConnected})
			e.saveState()
		} else {
			delete(e.pending, k)
		}
	case 'd':
		delete(e.pending, k)
	case 'e':
		if i := e.findOcc(k.id, k.start); i >= 0 {
			e.dropOcc(i)
			e.saveState()
		}
	}
	if list, err := e.Store.ListSchedules(); err == nil {
		e.finishOnce(list, now)
	}
}

// ActiveWindow is a connect window running now.
type ActiveWindow struct {
	ID    int64     `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

// Snapshot is the API's view of the schedules.
type Snapshot struct {
	Enabled      bool           `json:"enabled"`
	PausedReason string         `json:"pausedReason"`
	PausedAt     *time.Time     `json:"pausedAt"`
	Timezone     string         `json:"timezone"`
	Now          time.Time      `json:"now"`
	Schedules    []Schedule     `json:"schedules"`
	Active       []ActiveWindow `json:"active"`
	Upcoming     []Event        `json:"upcoming"`
}

// SnapshotEvents is the number of upcoming events in a Snapshot.
const SnapshotEvents = 10

// Snapshot returns the current state for the API and SSE.
func (e *Engine) Snapshot() Snapshot {
	e.mu.Lock()
	e.init()
	now := e.now()
	loc := e.loc()
	snap := Snapshot{
		Enabled: e.enabled, PausedReason: e.reason, Timezone: e.zone, Now: now.In(loc).Truncate(time.Second),
		Schedules: []Schedule{}, Active: []ActiveWindow{}, Upcoming: []Event{},
	}
	if !e.pausedAt.IsZero() {
		t := e.pausedAt.In(loc)
		snap.PausedAt = &t
	}
	state := append([]Occ(nil), e.state...)
	enabled := e.enabled
	e.mu.Unlock()

	list, err := e.Store.ListSchedules()
	if err != nil {
		e.logf("schedules: %v", err)
		return snap
	}
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Node != b.Node {
			return a.Node < b.Node
		}
		if a.Start != b.Start {
			return a.Start < b.Start
		}
		return a.ID < b.ID
	})
	snap.Schedules = list
	enabledIDs := map[int64]bool{}
	for _, s := range list {
		if s.Enabled {
			enabledIDs[s.ID] = true
		}
	}
	if enabled {
		for _, o := range state {
			if o.Status == StatusConnected && enabledIDs[o.ID] && !o.Start.After(now) && o.End.After(now) {
				snap.Active = append(snap.Active, ActiveWindow{ID: o.ID, Start: o.Start.In(loc), End: o.End.In(loc)})
			}
		}
		sort.Slice(snap.Active, func(i, j int) bool { return snap.Active[i].ID < snap.Active[j].ID })
	}
	snap.Upcoming = Upcoming(list, state, now, loc, SnapshotEvents)
	return snap
}

// String helps debugging.
func (o Occ) String() string {
	return fmt.Sprintf("%d@%s %s", o.ID, o.Start.Format(time.RFC3339), o.Status)
}
