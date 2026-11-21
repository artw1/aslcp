// Package dvswitch holds the server side of the DVSwitch feature that is not
// a helper op: the tracker that remembers the last tune of each mode and, when
// enabled, tunes there again after Analog_Bridge restarted (docs/DVSWITCH.md,
// "Tune and resume").
package dvswitch

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"regexp"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/helper"
)

const (
	// SettingKey is the settings-table key that holds the JSON State.
	SettingKey = "dvswitch.resume"
	// DefaultInterval is the poll period.
	DefaultInterval = 15 * time.Second
	// FastInterval is the poll period while a resume is still pending.
	FastInterval = 2 * time.Second
	// MaxBackoff caps the delay after helper errors.
	MaxBackoff = 60 * time.Second
	// DefaultWindow is how long after a start of Analog_Bridge a resume may
	// still happen.
	DefaultWindow = 3 * time.Minute
)

// ErrNotInstalled is what OnPoll gets when Installed says there is no DVSwitch.
var ErrNotInstalled = errors.New("dvswitch not installed")

// DefaultViewerInterval is the poll period while a client watches.
const DefaultViewerInterval = 5 * time.Second

// Source is the part of the helper the tracker uses.
type Source interface {
	DVSwitchInfo(ctx context.Context) (helper.DVSwitchInfo, error)
	DVSwitchTune(ctx context.Context, req helper.DVSwitchRequest) (helper.DVTuneResult, error)
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
	LastResumeFor string          `json:"lastResumeFor,omitempty"` // Analog_Bridge start already handled
}

// View is the reply of GET /api/dvswitch/resume.
type View struct {
	Resume   bool            `json:"resume"`
	LastMode string          `json:"lastMode,omitempty"`
	PerMode  map[string]Tune `json:"perMode"`
}

// Tracker polls the bridge, records the last tune per mode and resumes it.
// The zero value is not usable; fill Src, Store and Logger, then Run.
type Tracker struct {
	Src    Source
	Store  Settings
	Logger *log.Logger
	// Installed says whether DVSwitch is on this node; the poll does nothing
	// (and does not wake the helper) while it is false. Nil: always.
	Installed func() bool
	Now       func() time.Time
	// Interval, Fast and Window default to DefaultInterval, FastInterval and
	// DefaultWindow. Fast is used while a resume is pending (nextInterval).
	Interval, Fast, Window time.Duration
	// ViewerInterval (default 5 s) replaces Interval while Viewers reports at
	// least one connected client, so the dashboard card follows changes made
	// elsewhere (the Fast period of a pending resume still wins).
	ViewerInterval time.Duration
	// Viewers returns the number of connected SSE clients. Nil: none.
	Viewers func() int
	// OnPoll is called after every poll with what it read: err is the helper
	// error, or ErrNotInstalled when the node has no DVSwitch (info is empty
	// then and the helper was not asked). It runs in the poll goroutine.
	OnPoll func(info helper.DVSwitchInfo, err error)
	// BootMode, when set, makes DVSwitch start in the last mode: it is called
	// with LastMode when the tracker records a new one (and once per process
	// for the stored one), whether resume is on or off. A failure is retried
	// on the next poll.
	BootMode func(ctx context.Context, mode string) error
	// ModeEnabled, when set, says whether a mode is on (its card's Enable); a
	// resume into a disabled mode is skipped.
	ModeEnabled func(ctx context.Context, mode string) bool

	mu     sync.Mutex
	st     State
	loaded bool

	// Per-process observation (not stored): see record.
	baseKey           string
	baseSeen          bool
	baseTG, baseTS    string
	tuneSeen          bool
	tuneVal, tuneMode string
	modeSeen          bool   // a sample was recorded since the start
	prevMode          string // mode of the previous sample
	dmrHold           bool   // the DMR talkgroup is not trusted yet (see record)
	lastLogged        string
	bootSynced        string // the boot mode written by this process
	bootFailed        bool   // the last BootMode failed (logged once)

	// What the last poll saw, for nextInterval (guarded by mu).
	ps pollState
}

// pollState is what decides the delay before the next poll.
type pollState struct {
	Resume       bool      // the setting is on
	Active       bool      // analog_bridge runs
	Started      string    // its start time (RFC 3339), if known
	HandledFor   string    // the start already resumed or skipped
	Now          time.Time // time of the last poll
	PendingSince time.Time // first poll that saw a resume pending without a start (zero: none)
	Window       time.Duration
	Normal, Fast time.Duration
	Errors       int // consecutive helper errors
}

// pending says whether a resume may still happen: the setting is on and either
// analog_bridge is not active (it may start any moment, for at most the
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
// (never below the base, at most MaxBackoff) so a failing helper is not hammered.
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
		if helper.DVNormMode(m) == m && helper.DVValidTune(m, e.Tune) && !helper.DVPrivateTune(m, e.Tune) {
			clean[m] = e
		}
	}
	t.st.PerMode = clean
	if helper.DVNormMode(t.st.LastMode) != t.st.LastMode || t.st.LastMode != "" && clean[t.st.LastMode].Tune == "" {
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
// away and back, and a resume after a restart does not re-tune it. A later
// tune is remembered as usual.
func (t *Tracker) Forget(mode string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.load()
	t.forgetLocked(helper.DVNormMode(mode))
}

// forgetLocked is Forget; callers hold mu.
func (t *Tracker) forgetLocked(mode string) {
	if _, had := t.st.PerMode[mode]; !had {
		return
	}
	delete(t.st.PerMode, mode)
	if t.st.LastMode == mode {
		t.st.LastMode = ""
	}
	if err := t.save(); err != nil {
		t.logf("dvswitch tracker: cannot save the state: %v", err)
	}
}

// SetOnPoll and SetViewers wire the hooks; call them before Run.
func (t *Tracker) SetOnPoll(f func(helper.DVSwitchInfo, error)) { t.OnPoll = f }

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
	if t.Installed != nil && !t.Installed() {
		if t.OnPoll != nil {
			t.OnPoll(helper.DVSwitchInfo{}, ErrNotInstalled)
		}
		return
	}
	info, err := t.Src.DVSwitchInfo(ctx)
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
			t.logf("dvswitch tracker: cannot read the bridge state: %v", err)
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
	switch act {
	case resumeFire:
		_, err := t.Src.DVSwitchTune(ctx, helper.DVSwitchRequest{Author: "resume", Tune: &helper.DVTune{Mode: mode, Tune: tune}})
		if err != nil {
			t.logf("dvswitch resume: %s tune failed: %v", mode, err)
		} else {
			t.logf("dvswitch resume: %s tuned to the last used value", mode)
		}
		t.markResumed(info.Started)
		return // the next poll records the new state
	case resumeSkip:
		t.logf("dvswitch resume: skipped (%s)", why)
		t.markResumed(info.Started)
	}
	t.record(info, now)
	t.syncBoot(ctx)
}

// syncBoot passes a new LastMode to BootMode. The helper writes only what
// differs; this keeps it from being asked on every poll.
func (t *Tracker) syncBoot(ctx context.Context) {
	if t.BootMode == nil {
		return
	}
	t.mu.Lock()
	mode := t.st.LastMode
	done := mode == "" || mode == t.bootSynced
	t.mu.Unlock()
	if done {
		return
	}
	if err := t.BootMode(ctx, mode); err != nil {
		if !t.bootFailed {
			t.bootFailed = true
			t.logf("dvswitch tracker: cannot set the boot mode to %s: %v", mode, err)
		}
		return
	}
	t.mu.Lock()
	t.bootSynced, t.bootFailed = mode, false
	t.mu.Unlock()
}

// observe updates the poll state from a successful poll. Callers hold mu.
func (t *Tracker) observe(info helper.DVSwitchInfo, now time.Time) {
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
		t.logf("dvswitch resume: cannot save the state: %v", err)
	}
}

type resumeAction int

const (
	resumeNone resumeAction = iota // nothing to do (no reason to remember it)
	resumeWait                     // not yet: ABInfo of this start is missing
	resumeSkip                     // this start is handled without a tune
	resumeFire                     // tune now
)

// decideResume decides what to do about the current start of Analog_Bridge.
// It is pure: now, the window, the bridge state and the stored state.
//   - the setting is off, or the start time is unknown: nothing
//   - this start was handled already (LastResumeFor): nothing
//   - more than window since the start: skip
//   - no state file written by this start yet: wait
//   - no remembered tune for the last mode: skip
//   - ABInfo shows a tune already (the user tuned since the start): skip
//   - else: fire
func decideResume(now time.Time, window time.Duration, info helper.DVSwitchInfo, st State) (resumeAction, string) {
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
		return resumeWait, "no state file yet"
	}
	if up, err := time.Parse(time.RFC3339, info.Updated); err != nil || up.Before(started.Add(-2*time.Second)) {
		return resumeWait, "state file is from an earlier start"
	}
	e := st.PerMode[st.LastMode]
	if st.LastMode == "" || !helper.DVValidTune(st.LastMode, e.Tune) || helper.DVPrivateTune(st.LastMode, e.Tune) {
		return resumeSkip, "no remembered tune"
	}
	if info.Tune != "" {
		return resumeSkip, "already tuned since the start"
	}
	return resumeFire, ""
}

var reHostPort = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}:[0-9]{1,5}$`)
var reYSFID = regexp.MustCompile(`^[0-9]{5}$`)

// record remembers the current tune of the current mode.
//
//   - BM and DMR (both ambe_mode DMR, told apart by the panel mode,
//     helper.DVPanelMode): digital.tg (and ts). The startup talkgroup shown after a start is
//     not a tune: it counts once it differs from the first state seen after
//     the start, or once last_tune holds a value set in DMR.
//   - YSF, P25, NXDN: last_tune, when it is not empty and valid for the mode.
//     last_tune survives a mode change, so a value first seen in another mode
//     is not taken for this one.
//   - Right after a switch of mode to BM or DMR the talkgroup is not trusted:
//     Analog_Bridge takes txTg= of a YSF tune ("192.0.2.10:42000") as a number
//     and keeps its leading digits ("192") as digital.tg, so the first DMR
//     sample after a switch shows a value that is nobody's tune. It counts
//     only once last_tune changed after the switch and equals the
//     talkgroup, i.e. a tune that was sent in DMR (by the panel or an app).
//   - A DMR private call ("9990#", and the Parrot ID 9990 itself, which only
//     answers private calls) is never remembered: the remembered tune stays
//     the last real talkgroup, so a resume does not call a user again.
//   - Every value must pass the mode's validator (DMR: digits only, no prefix
//     parsing) before it is stored.
//   - Unlink values (DMR 4000, YSF disconnect, P25 and NXDN 9999) are not
//     remembered as a place to return to; they erase the remembered tune of
//     the mode (a disconnect is remembered, see Forget).
func (t *Tracker) record(info helper.DVSwitchInfo, now time.Time) {
	if !info.Found {
		return
	}
	mode := helper.DVPanelMode(info)
	t.mu.Lock()
	defer t.mu.Unlock()

	// Per-start bookkeeping (not stored).
	if !t.baseSeen || t.baseKey != info.Started {
		t.baseSeen, t.baseKey, t.baseTG, t.baseTS = true, info.Started, info.TG, info.Slot
		t.tuneSeen = false
		t.modeSeen, t.prevMode, t.dmrHold = false, "", false
	}
	if mode != t.prevMode {
		// a switch of mode (not the first sample of this start)
		t.dmrHold = t.modeSeen && helper.DVDMRFamily(mode)
		t.prevMode = mode
	}
	t.modeSeen = true
	if !t.tuneSeen || t.tuneVal != info.Tune {
		t.tuneSeen, t.tuneVal, t.tuneMode = true, info.Tune, mode
	}
	if mode == "" {
		return
	}

	var tune, ts string
	switch mode {
	case helper.DVModeBM, helper.DVModeDMR:
		if !helper.DVValidTune(mode, info.TG) {
			return
		}
		// A private call (Parrot 9990#) is a one-off, not the mode's target:
		// never remember it, whether ABInfo marks it (last_tune "9990#") or
		// only shows the called ID as the talkgroup.
		if info.Private || info.TG == helper.DVParrotID {
			return
		}
		if t.dmrHold {
			if info.Tune != info.TG || t.tuneMode != mode {
				return
			}
			t.dmrHold = false
		}
		moved := info.TG != t.baseTG || info.Slot != t.baseTS
		if !moved && !(info.Tune != "" && t.tuneMode == mode) {
			return
		}
		tune, ts = info.TG, info.Slot
	default:
		if info.Tune == "" || t.tuneMode != mode || !helper.DVValidTune(mode, info.Tune) {
			return
		}
		if mode == "YSF" && !helper.DVUnlinkValue(mode, info.Tune) && !reHostPort.MatchString(info.Tune) && !reYSFID.MatchString(info.Tune) {
			return
		}
		tune = info.Tune
	}
	if helper.DVUnlinkValue(mode, tune) {
		// The operator disconnected the target: forget it, so switching to
		// another mode and back does not re-tune the old one.
		t.forgetLocked(mode)
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
		t.logf("dvswitch tracker: cannot save the state: %v", err)
	}
}
