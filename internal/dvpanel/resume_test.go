package dvpanel

import (
	"context"
	"errors"
	"log"
	"strings"
	"sync"
	"testing"
	"time"
)

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// fresh is an engine that started at boot and has nothing tuned.
func fresh(boot time.Time) Info {
	return Info{Found: true, Started: boot.UTC().Format(time.RFC3339), Updated: boot.Add(5 * time.Second).UTC().Format(time.RFC3339)}
}

// tuned is an engine in mode with the target tune.
func tuned(boot time.Time, mode, tune, slot string) Info {
	i := fresh(boot)
	i.Mode, i.Tune, i.Slot = mode, tune, slot
	if DMRFamily(mode) {
		i.TG = tune
	}
	return i
}

type fakeStore struct {
	mu sync.Mutex
	m  map[string]string
}

func (f *fakeStore) GetSetting(k string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if v, ok := f.m[k]; ok {
		return v, nil
	}
	return "", errors.New("not found")
}
func (f *fakeStore) SetSetting(k, v string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]string{}
	}
	f.m[k] = v
	return nil
}

type tuneCall struct{ Mode, Tune string }

type fakeSrc struct {
	info  Info
	err   error
	tunes []tuneCall
	terr  error
	modes []string
}

func (f *fakeSrc) Mode(_ context.Context, mode string) (bool, error) {
	f.modes = append(f.modes, mode)
	return f.terr == nil, f.terr
}

func (f *fakeSrc) Info(context.Context) (Info, error) { return f.info, f.err }
func (f *fakeSrc) Tune(_ context.Context, mode, tune string) (bool, error) {
	f.tunes = append(f.tunes, tuneCall{mode, tune})
	return f.terr == nil, f.terr
}

func newTracker(t *testing.T) (*Tracker, *fakeSrc, *fakeStore, *time.Time) {
	t.Helper()
	src, st, now := &fakeSrc{}, &fakeStore{}, t0
	tr := &Tracker{Src: src, Store: st, Now: func() time.Time { return now }}
	return tr, src, st, &now
}

func newLogger(w *strings.Builder) *log.Logger { return log.New(w, "", 0) }

func resumeState(on bool) State {
	return State{Resume: on, LastMode: "YSF", PerMode: map[string]Tune{"YSF": {Tune: "192.0.2.10:42000", At: "2026-11-17T10:00:00Z"}, "DMR": {Tune: "1997", TS: "1"}}}
}

func TestRecordDMRAndOthers(t *testing.T) {
	tr, src, _, now := newTracker(t)
	ctx := context.Background()
	boot := t0.Add(-time.Hour)
	src.info = fresh(boot)
	tr.Poll(ctx)
	if v := tr.Get(); len(v.PerMode) != 0 {
		t.Fatalf("an engine with no target was remembered: %+v", v)
	}
	src.info = tuned(boot, "DMR", "1997", "2")
	tr.Poll(ctx)
	if e := tr.Get().PerMode["DMR"]; e.Tune != "1997" || e.TS != "2" || e.At != t0.Format(time.RFC3339) {
		t.Fatalf("%+v", e)
	}
	*now = t0.Add(time.Minute)
	src.info = tuned(boot, "YSF", "192.0.2.10:42000", "")
	tr.Poll(ctx)
	v := tr.Get()
	if v.LastMode != "YSF" || v.PerMode["YSF"].Tune != "192.0.2.10:42000" || v.PerMode["YSF"].TS != "" || v.PerMode["DMR"].Tune != "1997" {
		t.Fatalf("%+v", v)
	}
	// the time of a value moves only when the value changes
	*now = t0.Add(2 * time.Minute)
	tr.Poll(ctx)
	if e := tr.Get().PerMode["YSF"]; e.At != t0.Add(time.Minute).Format(time.RFC3339) {
		t.Fatalf("%+v", e)
	}
}

func TestRecordIgnoresLoopInvalidAndPrivate(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	ctx := context.Background()
	boot := t0.Add(-time.Hour)
	for _, i := range []Info{
		tuned(boot, "LOOP", "9990", ""),
		tuned(boot, "DMR", "12x", "1"),
		tuned(boot, "P25", "-5", ""),
		tuned(boot, "XYZ", "5", ""),
		tuned(boot, "DMR", ParrotID, "1"),
	} {
		src.info = i
		tr.Poll(ctx)
	}
	src.info = tuned(boot, "DMR", "1998", "1")
	src.info.Private = true
	tr.Poll(ctx)
	if v := tr.Get(); len(v.PerMode) != 0 {
		t.Fatalf("%+v", v)
	}
	// a real talkgroup, then a private call: the talkgroup stays
	src.info = tuned(boot, "DMR", "1997", "1")
	tr.Poll(ctx)
	src.info = tuned(boot, "DMR", ParrotID, "1")
	src.info.Private = true
	tr.Poll(ctx)
	if e := tr.Get().PerMode["DMR"]; e.Tune != "1997" {
		t.Fatalf("private call replaced the tune: %+v", e)
	}
}

func TestLoadDropsStoredPrivateAndInvalidTunes(t *testing.T) {
	st := &fakeStore{m: map[string]string{SettingKey: `{"lastMode":"DMR","perMode":{"DMR":{"tune":"9990#","at":"x"},"P25":{"tune":"abc"},"LOOP":{"tune":"9990"},"YSF":{"tune":"192.0.2.10:42000"}},"resume":true}`}}
	tr := &Tracker{Src: &fakeSrc{}, Store: st}
	v := tr.Get()
	if len(v.PerMode) != 2 || v.PerMode["YSF"].Tune == "" || v.LastMode != "DMR" {
		t.Fatalf("%+v", v)
	}
}

func TestPollSkippedWhenNotEnabled(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	tr.Enabled = func() bool { return false }
	src.err = errors.New("must not be asked")
	var got error
	tr.OnPoll = func(_ Info, err error) { got = err }
	tr.Poll(context.Background())
	if !errors.Is(got, ErrNotEnabled) {
		t.Fatalf("%v", got)
	}
}

func TestStatePersistsAcrossTrackers(t *testing.T) {
	tr, src, st, _ := newTracker(t)
	src.info = tuned(t0.Add(-time.Hour), "P25", "10100", "")
	tr.Poll(context.Background())
	_ = tr.SetResume(true)
	tr2 := &Tracker{Src: src, Store: st}
	if v := tr2.Get(); !v.Resume || v.LastMode != "P25" || v.PerMode["P25"].Tune != "10100" {
		t.Fatalf("%+v", v)
	}
}

func TestDecideResume(t *testing.T) {
	boot := t0.Add(-30 * time.Second)
	f := fresh(boot)
	retuned := tuned(boot, "DMR", "1998", "2")
	noInfo := Info{Started: boot.Format(time.RFC3339)}
	stale := f
	stale.Updated = boot.Add(-time.Hour).Format(time.RFC3339)
	handled := resumeState(true)
	handled.LastResumeFor = f.Started
	noTune := resumeState(true)
	noTune.LastMode, noTune.PerMode = "", nil
	unlinked := resumeState(true)
	delete(unlinked.PerMode, "YSF")
	inMode := fresh(boot)
	inMode.Mode = "YSF"
	tunedOther := tuned(boot, "DMR", "1998", "2")
	older := resumeState(true)
	older.LastResumeFor = "2026-11-17T08:00:00Z"
	unknown := f
	unknown.Started = ""

	cases := []struct {
		name string
		now  time.Time
		info Info
		st   State
		want resumeAction
	}{
		{"new start, nothing tuned: fire", t0, f, resumeState(true), resumeFire},
		{"an older start was handled: fire", t0, f, older, resumeFire},
		{"setting off", t0, f, resumeState(false), resumeNone},
		{"already resumed for this start", t0, f, handled, resumeNone},
		{"user already retuned", t0, retuned, resumeState(true), resumeSkip},
		{"too late (4 min after the start)", boot.Add(4 * time.Minute), f, resumeState(true), resumeSkip},
		{"just inside the window", boot.Add(179 * time.Second), f, resumeState(true), resumeFire},
		{"no remembered tune", t0, f, noTune, resumeSkip},
		{"last mode unlinked: select the mode", t0, f, unlinked, resumeMode},
		{"unlinked, engine in the mode already", t0, inMode, unlinked, resumeSkip},
		{"unlinked, user tuned since the start", t0, tunedOther, unlinked, resumeSkip},
		{"unlinked, too late", boot.Add(4 * time.Minute), f, unlinked, resumeSkip},
		{"no state yet: wait", t0, noInfo, resumeState(true), resumeWait},
		{"state of an earlier start: wait", t0, stale, resumeState(true), resumeWait},
		{"start time unknown", t0, unknown, resumeState(true), resumeNone},
		{"engine not running", t0, Info{}, resumeState(true), resumeNone},
	}
	for _, c := range cases {
		if got, why := decideResume(c.now, DefaultWindow, c.info, c.st); got != c.want {
			t.Errorf("%s: got %d (%s), want %d", c.name, got, why, c.want)
		}
	}
}

func TestResumeFiresOncePerStart(t *testing.T) {
	tr, src, _, now := newTracker(t)
	ctx := context.Background()
	tr.Poll(ctx)
	_ = tr.SetResume(true)
	boot := t0.Add(-20 * time.Second)
	tr.mu.Lock()
	tr.st = resumeState(true)
	tr.mu.Unlock()

	// the start is seen before its state exists: wait
	src.info = Info{Started: boot.Format(time.RFC3339)}
	tr.Poll(ctx)
	if len(src.tunes) != 0 {
		t.Fatal("tuned before the state existed")
	}
	src.info = fresh(boot)
	*now = t0.Add(10 * time.Second)
	tr.Poll(ctx)
	if len(src.tunes) != 1 || src.tunes[0] != (tuneCall{"YSF", "192.0.2.10:42000"}) {
		t.Fatalf("%+v", src.tunes)
	}
	if tr.Get().PerMode["DMR"].Tune != "1997" {
		t.Fatal("remembered tune lost")
	}
	for i := 0; i < 3; i++ {
		*now = now.Add(15 * time.Second)
		tr.Poll(ctx)
	}
	if len(src.tunes) != 1 {
		t.Fatalf("tuned %d times", len(src.tunes))
	}
	// a restart of aslcp: the same start is not resumed again
	tr2 := &Tracker{Src: src, Store: tr.Store, Now: tr.Now}
	tr2.Poll(ctx)
	if len(src.tunes) != 1 {
		t.Fatalf("tuned again after an aslcp restart: %d", len(src.tunes))
	}
	// a new start of the engine: again
	boot2 := now.Add(time.Second)
	src.info = fresh(boot2)
	*now = boot2.Add(20 * time.Second)
	tr2.Poll(ctx)
	if len(src.tunes) != 2 {
		t.Fatalf("new start not resumed: %d", len(src.tunes))
	}
}

func TestResumeAfterRebootOnFirstPoll(t *testing.T) {
	tr, src, st, _ := newTracker(t)
	b := `{"lastMode":"P25","perMode":{"P25":{"tune":"10100","at":"2026-11-17T10:00:00Z"}},"resume":true,"lastResumeFor":"2026-11-17T08:00:00Z"}`
	_ = st.SetSetting(SettingKey, b)
	src.info = fresh(t0.Add(-45 * time.Second))
	tr.Poll(context.Background())
	if len(src.tunes) != 1 || src.tunes[0] != (tuneCall{"P25", "10100"}) {
		t.Fatalf("%+v", src.tunes)
	}
}

func TestResumeFailureIsNotRetried(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	tr.st = resumeState(true)
	tr.loaded = true
	src.terr = errors.New("not_available")
	var log strings.Builder
	tr.Logger = newLogger(&log)
	src.info = fresh(t0.Add(-20 * time.Second))
	tr.Poll(context.Background())
	tr.Poll(context.Background())
	if len(src.tunes) != 1 || !strings.Contains(log.String(), "tune failed") {
		t.Fatalf("%d tunes, log %q", len(src.tunes), log.String())
	}
}

func TestResumeSkipIsMarked(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	tr.st = resumeState(true)
	tr.loaded = true
	var log strings.Builder
	tr.Logger = newLogger(&log)
	src.info = tuned(t0.Add(-20*time.Second), "DMR", "1998", "2") // the user tuned already
	tr.Poll(context.Background())
	tr.Poll(context.Background())
	if len(src.tunes) != 0 || strings.Count(log.String(), "skipped") != 1 {
		t.Fatalf("%d tunes, log %q", len(src.tunes), log.String())
	}
	if tr.st.LastResumeFor != src.info.Started {
		t.Fatal("start not marked")
	}
}

func TestNextInterval(t *testing.T) {
	base := pollState{Resume: true, Now: t0, Window: DefaultWindow, Normal: DefaultInterval, Fast: FastInterval}
	with := func(f func(*pollState)) pollState { s := base; f(&s); return s }
	boot := t0.Add(-20 * time.Second).UTC().Format(time.RFC3339)
	old := t0.Add(-4 * time.Minute).UTC().Format(time.RFC3339)
	cases := []struct {
		name string
		s    pollState
		want time.Duration
	}{
		{"setting off", with(func(s *pollState) { s.Resume = false }), 15 * time.Second},
		{"engine down, just noticed", with(func(s *pollState) { s.PendingSince = t0 }), 2 * time.Second},
		{"engine down, window passed", with(func(s *pollState) { s.PendingSince = t0.Add(-4 * time.Minute) }), 15 * time.Second},
		{"engine down, never noticed", base, 15 * time.Second},
		{"new start not yet resumed", with(func(s *pollState) { s.Active, s.Started = true, boot }), 2 * time.Second},
		{"start handled", with(func(s *pollState) { s.Active, s.Started, s.HandledFor = true, boot, boot }), 15 * time.Second},
		{"start older than the window", with(func(s *pollState) { s.Active, s.Started = true, old }), 15 * time.Second},
		{"start time unknown", with(func(s *pollState) { s.Active = true }), 15 * time.Second},
		{"fast, 1 error", with(func(s *pollState) { s.PendingSince, s.Errors = t0, 1 }), 4 * time.Second},
		{"fast, 3 errors", with(func(s *pollState) { s.PendingSince, s.Errors = t0, 3 }), 16 * time.Second},
		{"normal, 2 errors", with(func(s *pollState) { s.Errors = 2 }), 60 * time.Second},
		{"many errors are capped", with(func(s *pollState) { s.Errors = 50 }), MaxBackoff},
	}
	for _, c := range cases {
		if got := nextInterval(c.s); got != c.want {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestPollDrivesInterval(t *testing.T) {
	tr, src, _, now := newTracker(t)
	ctx := context.Background()
	_ = tr.SetResume(true)
	tr.Poll(ctx) // engine not running
	if d := tr.delay(); d != FastInterval {
		t.Fatalf("down: %v", d)
	}
	*now = now.Add(4 * time.Minute)
	tr.Poll(ctx)
	if d := tr.delay(); d != DefaultInterval {
		t.Fatalf("down past the window: %v", d)
	}
	src.info = fresh(now.Add(-5 * time.Second))
	_ = tr.SetResume(false)
	_ = tr.SetResume(true)
	tr.Poll(ctx)
	if d := tr.delay(); d != DefaultInterval {
		t.Fatalf("after the resume: %v (tunes %d)", d, len(src.tunes))
	}
	src.err = errors.New("engine down")
	tr.Poll(ctx)
	tr.Poll(ctx)
	if d := tr.delay(); d != 4*DefaultInterval {
		t.Fatalf("backoff: %v", d)
	}
	src.err = nil
	tr.Poll(ctx)
	if d := tr.delay(); d != DefaultInterval {
		t.Fatalf("reset: %v", d)
	}
}

func TestFirstPollIsFastWhenResumePending(t *testing.T) {
	tr, src, st, _ := newTracker(t)
	_ = st.SetSetting(SettingKey, `{"resume":true,"perMode":{}}`)
	src.info = fresh(t0.Add(-10 * time.Second))
	tr.Poll(context.Background()) // no remembered tune: skipped and marked
	if d := tr.delay(); d != DefaultInterval {
		t.Fatalf("%v", d)
	}
	tr2, src2, st2, _ := newTracker(t)
	_ = st2.SetSetting(SettingKey, `{"resume":true,"perMode":{}}`)
	src2.info = Info{}
	tr2.Poll(context.Background())
	if d := tr2.delay(); d != FastInterval {
		t.Fatalf("%v", d)
	}
}

func TestViewersShortenThePoll(t *testing.T) {
	tr, _, _, _ := newTracker(t)
	tr.Poll(context.Background())
	if d := tr.delay(); d != DefaultInterval {
		t.Fatalf("no viewers: %v", d)
	}
	n := 0
	tr.SetViewers(func() int { return n })
	if d := tr.delay(); d != DefaultInterval {
		t.Fatalf("viewers 0: %v", d)
	}
	n = 2
	if d := tr.delay(); d != DefaultViewerInterval {
		t.Fatalf("viewers 2: %v", d)
	}
	tr.ViewerInterval = time.Hour
	if d := tr.delay(); d != DefaultInterval {
		t.Fatalf("slow viewer interval: %v", d)
	}
}

func TestOnPollReports(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	var got []error
	var infos []Info
	tr.SetOnPoll(func(i Info, err error) { infos, got = append(infos, i), append(got, err) })
	src.info = tuned(t0, "P25", "10100", "")
	tr.Poll(context.Background())
	src.err = errors.New("down")
	tr.Poll(context.Background())
	tr.Enabled = func() bool { return false }
	tr.Poll(context.Background())
	if len(got) != 3 || got[0] != nil || infos[0].Mode != "P25" || got[1] == nil || !errors.Is(got[2], ErrNotEnabled) {
		t.Fatalf("%v %+v", got, infos)
	}
}

func TestDisconnectForgetsTune(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	ctx := context.Background()
	boot := t0.Add(-time.Hour)
	src.info = tuned(boot, "DMR", "1997", "2")
	tr.Poll(ctx)
	if tr.Get().PerMode["DMR"].Tune != "1997" {
		t.Fatal("not remembered")
	}
	src.info = tuned(boot, "DMR", "4000", "2")
	tr.Poll(ctx)
	if v := tr.Get(); len(v.PerMode) != 0 || v.LastMode != "DMR" {
		t.Fatalf("still remembered: %+v", v)
	}
	src.info = tuned(boot, "DMR", "1998", "1")
	tr.Poll(ctx)
	if e := tr.Get().PerMode["DMR"]; e.Tune != "1998" {
		t.Fatalf("%+v", e)
	}
	tr.Forget("DMR")
	if len(tr.Get().PerMode) != 0 {
		t.Fatal("Forget kept it")
	}
	src.info = tuned(boot, "YSF", "192.0.2.10:42000", "")
	tr.Poll(ctx)
	src.info = tuned(boot, "YSF", "disconnect", "")
	tr.Poll(ctx)
	if _, ok := tr.Get().PerMode["YSF"]; ok {
		t.Fatal("YSF disconnect not remembered")
	}
}

func TestResumeSkipsDisabledMode(t *testing.T) {
	tr, src, _, now := newTracker(t)
	var w strings.Builder
	tr.Logger = newLogger(&w)
	tr.mu.Lock()
	tr.st = resumeState(true)
	tr.loaded = true
	tr.mu.Unlock()
	tr.ModeEnabled = func(_ context.Context, m string) bool { return m != "YSF" }
	src.info = fresh(t0.Add(-20 * time.Second))
	*now = t0.Add(10 * time.Second)
	tr.Poll(context.Background())
	if len(src.tunes) != 0 || !strings.Contains(w.String(), "disabled") {
		t.Fatalf("%+v %s", src.tunes, w.String())
	}
}

func TestResumeTGIFOwnKey(t *testing.T) {
	tr, src, st, now := newTracker(t)
	ctx := context.Background()
	st.m = map[string]string{SettingKey: `{"lastMode":"TGIF","perMode":{"TGIF":{"tune":"1997","ts":"2"},"DMR":{"tune":"1998","ts":"2"}},"resume":true}`}
	boot := t0.Add(-20 * time.Second)
	src.info = fresh(boot)
	*now = t0.Add(10 * time.Second)
	tr.Poll(ctx)
	if len(src.tunes) != 1 || src.tunes[0] != (tuneCall{"TGIF", "1997"}) {
		t.Fatalf("%+v", src.tunes)
	}
	src.info = tuned(boot, "TGIF", "1999", "2")
	*now = now.Add(15 * time.Second)
	tr.Poll(ctx)
	if v := tr.Get(); v.PerMode["TGIF"].Tune != "1999" || v.PerMode["DMR"].Tune != "1998" || v.LastMode != "TGIF" {
		t.Fatalf("%+v", v)
	}
}

func TestUnlinkThenRestartResumesModeOnly(t *testing.T) {
	tr, src, _, now := newTracker(t)
	var w strings.Builder
	tr.Logger = newLogger(&w)
	ctx := context.Background()
	_ = tr.SetResume(true)
	boot := t0.Add(-time.Hour)
	src.info = tuned(boot, "YSF", "192.0.2.10:42000", "")
	tr.Poll(ctx)
	src.info = tuned(boot, "YSF", "disconnect", "")
	tr.Poll(ctx)
	if v := tr.Get(); v.LastMode != "YSF" || len(v.PerMode) != 0 {
		t.Fatalf("%+v", v)
	}
	// aslcp restarts too: the state comes from the store
	tr2 := &Tracker{Src: src, Store: tr.Store, Now: tr.Now, Logger: tr.Logger}
	boot2 := t0.Add(time.Minute)
	src.info = fresh(boot2)
	*now = boot2.Add(10 * time.Second)
	for i := 0; i < 3; i++ {
		tr2.Poll(ctx)
		*now = now.Add(15 * time.Second)
	}
	if len(src.tunes) != 0 {
		t.Fatalf("a target was sent: %+v", src.tunes)
	}
	if len(src.modes) != 1 || src.modes[0] != "YSF" || !strings.Contains(w.String(), "YSF selected, nothing linked") {
		t.Fatalf("%v %s", src.modes, w.String())
	}
}

func TestResumeModeOnlySkipsDisabledMode(t *testing.T) {
	tr, src, _, now := newTracker(t)
	st := resumeState(true)
	delete(st.PerMode, "YSF")
	tr.mu.Lock()
	tr.st, tr.loaded = st, true
	tr.mu.Unlock()
	tr.ModeEnabled = func(_ context.Context, m string) bool { return m != "YSF" }
	src.info = fresh(t0.Add(-20 * time.Second))
	*now = t0.Add(10 * time.Second)
	tr.Poll(context.Background())
	if len(src.modes) != 0 || len(src.tunes) != 0 {
		t.Fatalf("%v %+v", src.modes, src.tunes)
	}
}
