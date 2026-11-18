// Package dvswitch holds the server side of the DVSwitch feature that is not
// a helper op: the tracker that remembers the last tune of each mode and, when
// enabled, tunes there again after Analog_Bridge restarted (docs/DVSWITCH.md,
// "Tune and resume").
package dvswitch

import (
	"context"
	"encoding/json"
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

	mu     sync.Mutex
	st     State
	loaded bool

	// Per-process observation (not stored): see record.
	baseKey           string
	baseSeen          bool
	baseTG, baseTS    string
	tuneSeen          bool
	tuneVal, tuneMode string
	lastLogged        string

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
		if helper.DVNormMode(m) == m && helper.DVValidTune(m, e.Tune) {
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
	if s.Fast <= 0 {
		s.Fast = FastInterval
	}
	return nextInterval(s)
}

// Poll is one round: read the state, resume if due, record.
func (t *Tracker) Poll(ctx context.Context) {
	if t.Installed != nil && !t.Installed() {
		return
	}
	info, err := t.Src.DVSwitchInfo(ctx)
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
	if st.LastMode == "" || !helper.DVValidTune(st.LastMode, e.Tune) {
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
//   - DMR: digital.tg (and ts). The startup talkgroup shown after a start is
//     not a tune: it counts once it differs from the first state seen after
//     the start, or once last_tune holds a value set in DMR.
//   - YSF, P25, NXDN: last_tune, when it is not empty and valid for the mode.
//     last_tune survives a mode change, so a value first seen in another mode
//     is not taken for this one.
//   - Unlink values (DMR 4000, YSF disconnect, P25 and NXDN 9999) are not
//     remembered.
func (t *Tracker) record(info helper.DVSwitchInfo, now time.Time) {
	if !info.Found {
		return
	}
	mode := helper.DVNormMode(info.Mode)
	t.mu.Lock()
	defer t.mu.Unlock()

	// Per-start bookkeeping (not stored).
	if !t.baseSeen || t.baseKey != info.Started {
		t.baseSeen, t.baseKey, t.baseTG, t.baseTS = true, info.Started, info.TG, info.Slot
		t.tuneSeen = false
	}
	if !t.tuneSeen || t.tuneVal != info.Tune {
		t.tuneSeen, t.tuneVal, t.tuneMode = true, info.Tune, mode
	}
	if mode == "" {
		return
	}

	var tune, ts string
	switch mode {
	case "DMR":
		if !helper.DVValidTune("DMR", info.TG) {
			return
		}
		moved := info.TG != t.baseTG || info.Slot != t.baseTS
		if !moved && !(info.Tune != "" && t.tuneMode == "DMR") {
			return
		}
		tune, ts = info.TG, info.Slot
	default:
		if info.Tune == "" || t.tuneMode != mode || !helper.DVValidTune(mode, info.Tune) {
			return
		}
		if mode == "YSF" && !reHostPort.MatchString(info.Tune) && !reYSFID.MatchString(info.Tune) {
			return
		}
		tune = info.Tune
	}
	if helper.DVUnlinkValue(mode, tune) {
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
