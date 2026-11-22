// Package dvpanel holds the server side of the digital modes panel that is
// not a helper op: the tune validators, the tracker that remembers the last
// tune of each mode and, when enabled, tunes there again after the engine
// restarted (docs/DV.md, "Tune and resume").
// name for now.
package dvpanel

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"sync"
	"time"
)

const (
	// SettingKey is the settings-table key that holds the JSON State.
	SettingKey = "dv.resume"
	// DefaultInterval is the poll period.
	DefaultInterval = 15 * time.Second
	// FastInterval is the poll period while a resume is still pending.
	FastInterval = 2 * time.Second
	// MaxBackoff caps the delay after engine errors.
	MaxBackoff = 60 * time.Second
	// DefaultWindow is how long after a start of the engine a resume may
	// still happen.
	DefaultWindow = 3 * time.Minute
)

// ErrNotEnabled is what OnPoll gets when Enabled says digital modes are off.
var ErrNotEnabled = errors.New("digital modes not enabled")

// DefaultViewerInterval is the poll period while a client watches.
const DefaultViewerInterval = 5 * time.Second

// Source is the part of the engine client the tracker uses. Tune switches the
// mode and tunes; Mode only switches the mode and sends nothing to the
// network (the engine's mode op). The bool says whether the engine
// confirmed it.
type Source interface {
	Info(ctx context.Context) (Info, error)
	Tune(ctx context.Context, mode, tune string) (bool, error)
	Mode(ctx context.Context, mode string) (bool, error)
}

// Settings is the part of the database the tracker needs.
type Settings interface {
	GetSetting(k string) (string, error)
	SetSetting(k, v string) error
}

// Tune is the last tune seen in one mode. TS is the DMR slot (shown only).
type Tune struct {
	Tune string `json:"tune"`
	TS   string `json:"ts,omitempty"`
	At   string `json:"at"` // RFC 3339, UTC: when this value was first seen
}

// State is what is stored.
type State struct {
	LastMode      string          `json:"lastMode,omitempty"`
	PerMode       map[string]Tune `json:"perMode"`
	Resume        bool            `json:"resume"`
	LastResumeFor string          `json:"lastResumeFor,omitempty"` // engine start already handled
}

// View is the reply of GET /api/dv/resume.
type View struct {
	Resume   bool            `json:"resume"`
	LastMode string          `json:"lastMode,omitempty"`
	PerMode  map[string]Tune `json:"perMode"`
}

// Tracker polls the engine, records the last tune per mode and resumes it.
// The zero value is not usable; fill Src, Store and Logger, then Run.
type Tracker struct {
	Src    Source
	Store  Settings
	Logger *log.Logger
	// Enabled says whether digital modes are on; the poll does nothing while
	// it is false. Nil: always.
	Enabled func() bool
	Now     func() time.Time
	// Interval, Fast and Window default to DefaultInterval, FastInterval and
	// DefaultWindow. Fast is used while a resume is pending (nextInterval).
	Interval, Fast, Window time.Duration
	// ViewerInterval (default 5 s) replaces Interval while Viewers reports at
	// least one connected client, so the dashboard card follows changes made
	// elsewhere (the Fast period of a pending resume still wins).
	ViewerInterval time.Duration
	// Viewers returns the number of connected SSE clients. Nil: none.
	Viewers func() int
	// OnPoll is called after every poll with what it read: err is the engine
	// error, or ErrNotEnabled when digital modes are off (info is empty then
	// and the engine was not asked). It runs in the poll goroutine.
	OnPoll func(info Info, err error)
	// ModeEnabled, when set, says whether a mode is on (its card's Enable); a
	// resume into a disabled mode is skipped.
	ModeEnabled func(ctx context.Context, mode string) bool

	mu     sync.Mutex
	st     State
	loaded bool

	lastLogged string

	// What the last poll saw, for nextInterval (guarded by mu).
	ps pollState
}

// pollState is what decides the delay before the next poll.
type pollState struct {
	Resume       bool      // the setting is on
	Active       bool      // the engine runs
	Started      string    // its start time (RFC 3339), if known
	HandledFor   string    // the start already resumed or skipped
	Now          time.Time // time of the last poll
	PendingSince time.Time // first poll that saw a resume pending without a start (zero: none)
	Window       time.Duration
	Normal, Fast time.Duration
	Errors       int // consecutive engine errors
}

// pending says whether a resume may still happen: the setting is on and either
// the engine is not active (it may start any moment, for at most the
// window from when we first noticed), or it is active with a start that was
// not handled yet and is still inside the window.
func (s pollState) pending() bool {
	if !s.Resume {
		return false
	}
	if !s.Active {
		return !s.PendingSince.IsZero() && s.Now.Sub(s.PendingSince) <= s.Window
	}
	if s.Started == "" || s.Started == s.HandledFor {
		return false
	}
	st, err := time.Parse(time.RFC3339, s.Started)
	return err == nil && s.Now.Sub(st) <= s.Window
}

// nextInterval is the delay before the next poll: Fast while a resume is
// pending, Normal otherwise. After helper errors it doubles per error
// (never below the base, at most MaxBackoff) so a failing engine is not hammered.
func nextInterval(s pollState) time.Duration {
	d := s.Normal
	if s.pending() && s.Fast > 0 && s.Fast < d {
		d = s.Fast
	}
	for i := 0; i < s.Errors && d < MaxBackoff; i++ {
		d *= 2
	}
	if d > MaxBackoff && s.Errors > 0 {
		d = MaxBackoff
	}
	return d
}

func (t *Tracker) now() time.Time {
	if t.Now != nil {
		return t.Now()
	}
	return time.Now()
}

func (t *Tracker) logf(f string, a ...any) {
	if t.Logger != nil {
		t.Logger.Printf(f, a...)
	}
}

// load reads the stored state once. Callers hold mu.
func (t *Tracker) load() {
	if t.loaded {
		return
	}
	t.loaded = true
	if v, err := t.Store.GetSetting(SettingKey); err == nil && v != "" {
		var s State
		if json.Unmarshal([]byte(v), &s) == nil {
			t.st = s
		}
	}
	// A stored value is data: drop what the validators would refuse.
	clean := map[string]Tune{}
	for m, e := range t.st.PerMode {
		if NormMode(m) == m && ValidTune(m, e.Tune) && !PrivateTune(m, e.Tune) {
			clean[m] = e
		}
	}
	t.st.PerMode = clean
	// LastMode may have no tune: it is a mode whose target was unlinked.
	if NormMode(t.st.LastMode) != t.st.LastMode {
		t.st.LastMode = ""
	}
}

// save writes the state. Callers hold mu.
func (t *Tracker) save() error {
	b, err := json.Marshal(t.st)
	if err != nil {
		return err
	}
	return t.Store.SetSetting(SettingKey, string(b))
}

// Forget erases the remembered tune of a mode. It is what a disconnect (an
// unlink value) does: the mode stays disconnected when the operator switches
// away and back, and a resume after a restart does not re-tune it. The last
// mode is kept, so a resume still selects it, with nothing linked. A later
// tune is remembered as usual.
func (t *Tracker) Forget(mode string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.load()
	t.forgetLocked(NormMode(mode))
}

// forgetLocked is Forget; callers hold mu.
func (t *Tracker) forgetLocked(mode string) {
	if _, had := t.st.PerMode[mode]; !had {
		return
	}
	delete(t.st.PerMode, mode)
	if err := t.save(); err != nil {
		t.logf("dv tracker: cannot save the state: %v", err)
	}
}

// SetOnPoll and SetViewers wire the hooks; call them before Run.
func (t *Tracker) SetOnPoll(f func(Info, error)) { t.OnPoll = f }

// SetViewers sets the client counter (see Viewers).
func (t *Tracker) SetViewers(f func() int) { t.Viewers = f }

// Get returns the remembered tunes and the setting.
func (t *Tracker) Get() View {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.load()
	v := View{Resume: t.st.Resume, LastMode: t.st.LastMode, PerMode: map[string]Tune{}}
	for k, e := range t.st.PerMode {
		v.PerMode[k] = e
	}
	return v
}

// SetResume turns "resume after restart" on or off. It acts on a start only
// within the window after it (decideResume), so turning it on later leaves a
// bridge that has been running for a while alone.
func (t *Tracker) SetResume(on bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.load()
	if t.st.Resume == on {
		return nil
	}
	old := t.st.Resume
	t.st.Resume = on
	if err := t.save(); err != nil {
		t.st.Resume = old
		return err
	}
	return nil
}

// Run polls until ctx ends. It is one loop: a poll never starts before the
// previous one returned, so at most one request is in flight.
func (t *Tracker) Run(ctx context.Context) {
	for {
		t.Poll(ctx)
		tm := time.NewTimer(t.delay())
		select {
		case <-ctx.Done():
			tm.Stop()
			return
		case <-tm.C:
		}
	}
}

func (t *Tracker) delay() time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.ps
	s.Normal, s.Fast, s.Window = t.Interval, t.Fast, t.window()
	if s.Normal <= 0 {
		s.Normal = DefaultInterval
	}
	if t.Viewers != nil && t.Viewers() > 0 {
		v := t.ViewerInterval
		if v <= 0 {
			v = DefaultViewerInterval
		}
		if v < s.Normal {
			s.Normal = v
		}
	}
	if s.Fast <= 0 {
		s.Fast = FastInterval
	}
	return nextInterval(s)
}

// Poll is one round: read the state, resume if due, record.
func (t *Tracker) Poll(ctx context.Context) {
	if t.Enabled != nil && !t.Enabled() {
		if t.OnPoll != nil {
			t.OnPoll(Info{}, ErrNotEnabled)
		}
		return
	}
	info, err := t.Src.Info(ctx)
	if t.OnPoll != nil {
		// Reported last, after the resume and the recording of this poll.
		defer func() { t.OnPoll(info, err) }()
	}
	if err != nil {
		t.mu.Lock()
		t.ps.Errors++
		t.ps.Now = t.now()
		t.mu.Unlock()
		// log a failure once, not on every poll
		if t.lastLogged != "poll" {
			t.lastLogged = "poll"
			t.logf("dv tracker: cannot read the engine state: %v", err)
		}
		return
	}
	t.lastLogged = ""
	now := t.now()

	t.mu.Lock()
	t.load()
	act, why := decideResume(now, t.window(), info, t.st)
	mode, tune := t.st.LastMode, t.st.PerMode[t.st.LastMode].Tune
	t.observe(info, now)
	t.mu.Unlock()

	if act == resumeFire && t.ModeEnabled != nil && !t.ModeEnabled(ctx, mode) {
		act, why = resumeSkip, "the mode is disabled"
	}
	if act == resumeMode && t.ModeEnabled != nil && !t.ModeEnabled(ctx, mode) {
		act, why = resumeSkip, "the mode is disabled"
	}
	switch act {
	case resumeMode:
		if _, err := t.Src.Mode(ctx, mode); err != nil {
			t.logf("dv resume: %s mode switch failed: %v", mode, err)
		} else {
			t.logf("dv resume: %s selected, nothing linked", mode)
		}
		t.markResumed(info.Started)
		return
	case resumeFire:
		_, err := t.Src.Tune(ctx, mode, tune)
		if err != nil {
			t.logf("dv resume: %s tune failed: %v", mode, err)
		} else {
			t.logf("dv resume: %s tuned to the last used value", mode)
		}
		t.markResumed(info.Started)
		return // the next poll records the new state
	case resumeSkip:
		t.logf("dv resume: skipped (%s)", why)
		t.markResumed(info.Started)
	}
	t.record(info, now)
}

// observe updates the poll state from a successful poll. Callers hold mu.
func (t *Tracker) observe(info Info, now time.Time) {
	active := info.Found || info.Started != ""
	p := &t.ps
	p.Errors, p.Now = 0, now
	p.Resume, p.Active, p.Started, p.HandledFor = t.st.Resume, active, info.Started, t.st.LastResumeFor
	if t.st.Resume && !active {
		if p.PendingSince.IsZero() {
			p.PendingSince = now
		}
	} else {
		p.PendingSince = time.Time{}
	}
}

func (t *Tracker) window() time.Duration {
	if t.Window > 0 {
		return t.Window
	}
	return DefaultWindow
}

func (t *Tracker) markResumed(started string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.st.LastResumeFor = started
	t.ps.HandledFor = started
	if err := t.save(); err != nil {
		t.logf("dv resume: cannot save the state: %v", err)
	}
}

type resumeAction int

const (
	resumeNone resumeAction = iota // nothing to do (no reason to remember it)
	resumeWait                     // not yet: the engine state of this start is missing
	resumeSkip                     // this start is handled without a tune
	resumeFire                     // tune now
	resumeMode                     // switch to the last mode, nothing to tune
)

// decideResume decides what to do about the current start of the engine.
// It is pure: now, the window, the engine state and the stored state.
//   - the setting is off, or the start time is unknown: nothing
//   - this start was handled already (LastResumeFor): nothing
//   - more than window since the start: skip
//   - no state from this start yet: wait
//   - no last mode: skip
//   - no remembered tune for the last mode (it was unlinked): select the mode
//     only, unless the engine is in it already or shows a tune
//   - the engine shows a tune already (the user tuned since the start): skip
//   - else: fire
func decideResume(now time.Time, window time.Duration, info Info, st State) (resumeAction, string) {
	if !st.Resume {
		return resumeNone, "off"
	}
	started, err := time.Parse(time.RFC3339, info.Started)
	if err != nil {
		return resumeNone, "start time unknown"
	}
	if info.Started == st.LastResumeFor {
		return resumeNone, "already handled"
	}
	if now.Sub(started) > window {
		return resumeSkip, "too long after the start"
	}
	if !info.Found {
		return resumeWait, "no state yet"
	}
	if up, err := time.Parse(time.RFC3339, info.Updated); err != nil || up.Before(started.Add(-2*time.Second)) {
		return resumeWait, "state is from an earlier start"
	}
	e := st.PerMode[st.LastMode]
	if st.LastMode == "" {
		return resumeSkip, "no remembered tune"
	}
	if !ValidTune(st.LastMode, e.Tune) || PrivateTune(st.LastMode, e.Tune) {
		switch {
		case info.Tune != "":
			return resumeSkip, "already tuned since the start"
		case info.Mode == st.LastMode:
			return resumeSkip, "already in the last mode"
		}
		return resumeMode, ""
	}
	if info.Tune != "" {
		return resumeSkip, "already tuned since the start"
	}
	return resumeFire, ""
}

// record remembers the current tune of the current mode, as the engine shows
// it (the poll: changes made elsewhere than the dashboard).
func (t *Tracker) record(info Info, now time.Time) {
	if !info.Found || info.Tune == "" {
		return
	}
	ts := ""
	if DMRFamily(NormMode(info.Mode)) {
		ts = info.Slot
	}
	t.remember(info.Mode, info.Tune, ts, info.Private, now)
}

// RememberTune records a tune the dashboard has just sent and the engine
// accepted, at once: an engine that restarts seconds later must not lose it
// (the poll runs every 5 to 15 s). The rules are those of the poll.
func (t *Tracker) RememberTune(mode, tune, ts string) {
	t.remember(mode, tune, ts, false, t.now())
}

// RememberMode records that the operator switched to mode, keeping that
// mode's remembered tune. LOOP is a test mode and is not remembered.
func (t *Tracker) RememberMode(mode string) {
	mode = NormMode(mode)
	if mode == "" || mode == ModeLoop {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.load()
	t.setLastModeLocked(mode)
}

// setLastModeLocked sets the last mode and saves when it changed. Callers
// hold mu.
func (t *Tracker) setLastModeLocked(mode string) {
	if t.st.LastMode == mode {
		return
	}
	t.st.LastMode = mode
	if err := t.save(); err != nil {
		t.logf("dv tracker: cannot save the state: %v", err)
	}
}

// remember is the shared rule of record and RememberTune.
//
//   - Every value must pass the mode's validator before it is stored.
//   - LOOP is not remembered: the echo test is a test, not a place to return to.
//   - A DMR private call ("9990#", and the Parrot ID 9990 itself, which only
//     answers private calls) is never remembered: the remembered tune stays
//     the last real talkgroup, so a resume does not call a user again.
//   - Unlink values (DMR 4000, YSF disconnect, P25 and NXDN 9999) are not
//     remembered as a place to return to; they erase the remembered tune of
//     the mode (a disconnect is remembered, see Forget) and make it the last
//     mode.
func (t *Tracker) remember(mode, tune, ts string, private bool, now time.Time) {
	mode = NormMode(mode)
	if mode == "" || mode == ModeLoop || tune == "" {
		return
	}
	if private || (DMRFamily(mode) && tune == ParrotID) || PrivateTune(mode, tune) {
		return
	}
	if !DMRFamily(mode) {
		ts = ""
	}
	if !ValidTune(mode, tune) {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.load()
	if UnlinkValue(mode, tune) {
		// The operator disconnected the target: forget it, so switching to
		// another mode and back does not re-tune the old one.
		t.forgetLocked(mode)
		t.setLastModeLocked(mode)
		return
	}
	cur, had := t.st.PerMode[mode]
	if had && cur.Tune == tune && cur.TS == ts && t.st.LastMode == mode {
		return
	}
	if t.st.PerMode == nil {
		t.st.PerMode = map[string]Tune{}
	}
	at := cur.At
	if !had || cur.Tune != tune || at == "" {
		at = now.UTC().Format(time.RFC3339)
	}
	t.st.PerMode[mode] = Tune{Tune: tune, TS: ts, At: at}
	t.st.LastMode = mode
	if err := t.save(); err != nil {
		t.logf("dv tracker: cannot save the state: %v", err)
	}
}
