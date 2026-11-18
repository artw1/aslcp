package dvswitch

import (
	"context"
	"errors"
	"log"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/helper"
)

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

// sample builds a parsed ABInfo from the real sample with fields replaced.
func sample(t *testing.T, repl ...string) helper.DVSwitchInfo {
	t.Helper()
	b, err := os.ReadFile("../helper/testdata/ABInfo_31001.json")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for i := 0; i+1 < len(repl); i += 2 {
		if !strings.Contains(s, repl[i]) {
			t.Fatalf("sample has no %q", repl[i])
		}
		s = strings.Replace(s, repl[i], repl[i+1], 1)
	}
	info, err := helper.ParseABInfo([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func dmr(t *testing.T, tg, ts, last string) helper.DVSwitchInfo {
	return sample(t, `"tg":"9"`, `"tg":"`+tg+`"`, `"ts":"2"`, `"ts":"`+ts+`"`, `"last_tune":""`, `"last_tune":"`+last+`"`)
}

func other(t *testing.T, mode, last string) helper.DVSwitchInfo {
	return sample(t, `"ambe_mode":"DMR"`, `"ambe_mode":"`+mode+`"`, `"last_tune":""`, `"last_tune":"`+last+`"`)
}

func started(i helper.DVSwitchInfo, at time.Time) helper.DVSwitchInfo {
	i.Started = at.UTC().Format(time.RFC3339)
	i.Updated = at.Add(5 * time.Second).UTC().Format(time.RFC3339)
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

type fakeSrc struct {
	info  helper.DVSwitchInfo
	err   error
	tunes []helper.DVTune
	terr  error
}

func (f *fakeSrc) DVSwitchInfo(context.Context) (helper.DVSwitchInfo, error) { return f.info, f.err }
func (f *fakeSrc) DVSwitchTune(_ context.Context, r helper.DVSwitchRequest) (helper.DVTuneResult, error) {
	f.tunes = append(f.tunes, *r.Tune)
	return helper.DVTuneResult{Mode: r.Tune.Mode}, f.terr
}

func newTracker(t *testing.T) (*Tracker, *fakeSrc, *fakeStore, *time.Time) {
	t.Helper()
	src, st, now := &fakeSrc{}, &fakeStore{}, t0
	tr := &Tracker{Src: src, Store: st, Now: func() time.Time { return now }}
	return tr, src, st, &now
}

func TestRecordDMRSample(t *testing.T) {
	tr, src, _, now := newTracker(t)
	boot := t0.Add(-time.Hour)
	// fresh start: the stock tg 9 is the startup value, not a tune
	src.info = started(dmr(t, "9", "2", ""), boot)
	tr.Poll(context.Background())
	if v := tr.Get(); len(v.PerMode) != 0 || v.LastMode != "" {
		t.Fatalf("startup values were recorded: %+v", v)
	}
	// the user moves to TG 262 on slot 1
	src.info = started(dmr(t, "262", "1", "262"), boot)
	*now = t0.Add(time.Minute)
	tr.Poll(context.Background())
	v := tr.Get()
	e := v.PerMode["DMR"]
	if v.LastMode != "DMR" || e.Tune != "262" || e.TS != "1" || e.At != now.UTC().Format(time.RFC3339) {
		t.Fatalf("%+v", v)
	}
	// the same value again keeps the time it was first seen
	*now = t0.Add(5 * time.Minute)
	tr.Poll(context.Background())
	if got := tr.Get().PerMode["DMR"].At; got != e.At {
		t.Fatalf("time moved: %s -> %s", e.At, got)
	}
	// an explicit tune to the startup talkgroup counts (last_tune is set)
	src.info = started(dmr(t, "9", "2", "9"), boot)
	tr.Poll(context.Background())
	if e := tr.Get().PerMode["DMR"]; e.Tune != "9" {
		t.Fatalf("%+v", e)
	}
	// unlink (4000) is not a place to return to
	src.info = started(dmr(t, "4000", "2", "4000"), boot)
	tr.Poll(context.Background())
	if e := tr.Get().PerMode["DMR"]; e.Tune != "9" {
		t.Fatalf("%+v", e)
	}
}

func TestRecordDMRChangedWithoutLastTune(t *testing.T) {
	// a talkgroup set from an app does not touch last_tune
	tr, src, _, _ := newTracker(t)
	boot := t0.Add(-time.Hour)
	src.info = started(dmr(t, "9", "2", ""), boot)
	tr.Poll(context.Background())
	src.info = started(dmr(t, "91", "2", ""), boot)
	tr.Poll(context.Background())
	if e := tr.Get().PerMode["DMR"]; e.Tune != "91" {
		t.Fatalf("%+v", e)
	}
}

func TestRecordNonDMRSamples(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	boot := t0.Add(-time.Hour)
	ctx := context.Background()
	for _, c := range []struct{ mode, key, last string }{
		{"YSFN", "YSF", "192.0.2.10:42000"}, {"P25", "P25", "10100"}, {"NXDN", "NXDN", "10200"}, {"YSFW", "YSF", "198.51.100.7:42001"},
	} {
		src.info = started(other(t, c.mode, c.last), boot)
		tr.Poll(ctx)
		v := tr.Get()
		if v.LastMode != c.key || v.PerMode[c.key].Tune != c.last {
			t.Fatalf("%s: %+v", c.mode, v)
		}
	}
	if len(tr.Get().PerMode) != 3 {
		t.Fatalf("%+v", tr.Get().PerMode)
	}
}

func TestRecordIgnoresEmptyDefaultStaleAndInvalid(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	boot := t0.Add(-time.Hour)
	ctx := context.Background()
	// empty last_tune
	src.info = started(other(t, "P25", ""), boot)
	tr.Poll(ctx)
	// a P25 tune, then a switch to YSF with no tune: last_tune still holds
	// the P25 value, which is not a YSF tune
	src.info = started(other(t, "P25", "10100"), boot)
	tr.Poll(ctx)
	src.info = started(other(t, "YSFN", "10100"), boot)
	tr.Poll(ctx)
	// likewise a DMR talkgroup carried into NXDN
	src.info = started(dmr(t, "262", "1", "262"), boot)
	tr.Poll(ctx)
	src.info = started(other(t, "NXDN", "262"), boot)
	tr.Poll(ctx)
	// unlink values, a value that is not valid for the mode, an unknown mode
	src.info = started(other(t, "P25", "9999"), boot)
	tr.Poll(ctx)
	src.info = started(other(t, "YSFN", "disconnect"), boot)
	tr.Poll(ctx)
	src.info = started(other(t, "NXDN", "not-a-number"), boot)
	tr.Poll(ctx)
	src.info = started(other(t, "DSTAR", "10100"), boot)
	tr.Poll(ctx)
	v := tr.Get()
	if len(v.PerMode) != 2 || v.PerMode["P25"].Tune != "10100" || v.PerMode["DMR"].Tune != "262" || v.LastMode != "DMR" {
		t.Fatalf("%+v", v)
	}
	// the bridge not found: nothing changes, nothing breaks
	src.info = helper.DVSwitchInfo{}
	tr.Poll(ctx)
	src.err = errors.New("helper down")
	tr.Poll(ctx)
	if len(tr.Get().PerMode) != 2 {
		t.Fatal("state lost")
	}
}

func TestPollSkippedWhenNotInstalled(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	tr.Installed = func() bool { return false }
	src.info = started(other(t, "P25", "10100"), t0)
	tr.Poll(context.Background())
	if len(tr.Get().PerMode) != 0 {
		t.Fatal("polled")
	}
}

func TestStatePersistsAcrossTrackers(t *testing.T) {
	tr, src, st, _ := newTracker(t)
	src.info = started(other(t, "P25", "10100"), t0.Add(-time.Hour))
	tr.Poll(context.Background())
	if err := tr.SetResume(true); err != nil {
		t.Fatal(err)
	}
	tr2 := &Tracker{Src: src, Store: st}
	if v := tr2.Get(); !v.Resume || v.PerMode["P25"].Tune != "10100" || v.LastMode != "P25" {
		t.Fatalf("%+v", v)
	}
	// stored garbage is dropped
	st.m[SettingKey] = `{"lastMode":"P25","perMode":{"P25":{"tune":"x;y"},"BAD":{"tune":"1"}},"resume":true}`
	tr3 := &Tracker{Src: src, Store: st}
	if v := tr3.Get(); len(v.PerMode) != 0 || v.LastMode != "" || !v.Resume {
		t.Fatalf("%+v", v)
	}
}

// ---- resume

func resumeState(on bool) State {
	return State{Resume: on, LastMode: "YSF", PerMode: map[string]Tune{"YSF": {Tune: "192.0.2.10:42000", At: "2026-11-17T10:00:00Z"}, "DMR": {Tune: "262", TS: "1"}}}
}

func TestDecideResume(t *testing.T) {
	boot := t0.Add(-30 * time.Second)
	fresh := started(dmr(t, "9", "2", ""), boot) // a start that has written ABInfo, nothing tuned
	retuned := started(dmr(t, "91", "2", "91"), boot)
	noFile := helper.DVSwitchInfo{Started: boot.Format(time.RFC3339)}
	stale := fresh
	stale.Updated = boot.Add(-time.Hour).Format(time.RFC3339)
	handled := resumeState(true)
	handled.LastResumeFor = fresh.Started
	noTune := resumeState(true)
	noTune.LastMode, noTune.PerMode = "", nil
	other := resumeState(true)
	other.LastResumeFor = "2026-11-17T08:00:00Z"
	unknown := fresh
	unknown.Started = ""

	cases := []struct {
		name string
		now  time.Time
		info helper.DVSwitchInfo
		st   State
		want resumeAction
	}{
		{"new start, nothing tuned: fire", t0, fresh, resumeState(true), resumeFire},
		{"new start, an older start was handled: fire", t0, fresh, other, resumeFire},
		{"setting off", t0, fresh, resumeState(false), resumeNone},
		{"already resumed for this start", t0, fresh, handled, resumeNone},
		{"user already retuned", t0, retuned, resumeState(true), resumeSkip},
		{"too late (4 min after the start)", boot.Add(4 * time.Minute), fresh, resumeState(true), resumeSkip},
		{"just inside the window", boot.Add(179 * time.Second), fresh, resumeState(true), resumeFire},
		{"no remembered tune", t0, fresh, noTune, resumeSkip},
		{"no ABInfo yet: wait", t0, noFile, resumeState(true), resumeWait},
		{"ABInfo of an earlier start: wait", t0, stale, resumeState(true), resumeWait},
		{"start time unknown", t0, unknown, resumeState(true), resumeNone},
		{"bridge not running", t0, helper.DVSwitchInfo{}, resumeState(true), resumeNone},
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
	// an earlier session left a remembered tune
	tr.mu.Lock()
	tr.st = resumeState(true)
	tr.mu.Unlock()

	// the start is seen before ABInfo exists: wait
	src.info = helper.DVSwitchInfo{Started: boot.Format(time.RFC3339)}
	tr.Poll(ctx)
	if len(src.tunes) != 0 {
		t.Fatal("tuned before ABInfo existed")
	}
	// ABInfo appears with the startup values: one tune
	src.info = started(dmr(t, "9", "2", ""), boot)
	*now = t0.Add(10 * time.Second)
	tr.Poll(ctx)
	if len(src.tunes) != 1 || src.tunes[0] != (helper.DVTune{Mode: "YSF", Tune: "192.0.2.10:42000"}) {
		t.Fatalf("%+v", src.tunes)
	}
	if tr.Get().PerMode["DMR"].Tune != "262" {
		t.Fatal("remembered tune lost by the startup values")
	}
	// more polls of the same start: no second tune, even if ABInfo is still empty
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
	// a new start of the bridge: again
	boot2 := now.Add(time.Second)
	src.info = started(dmr(t, "9", "2", ""), boot2)
	*now = boot2.Add(20 * time.Second)
	tr2.Poll(ctx)
	if len(src.tunes) != 2 {
		t.Fatalf("new start not resumed: %d", len(src.tunes))
	}
}

func TestResumeAfterRebootOnFirstPoll(t *testing.T) {
	// aslcp starts after the reboot: its first poll sees a start it has not resumed
	tr, src, st, _ := newTracker(t)
	b := `{"lastMode":"P25","perMode":{"P25":{"tune":"10100","at":"2026-11-17T10:00:00Z"}},"resume":true,"lastResumeFor":"2026-11-17T08:00:00Z"}`
	_ = st.SetSetting(SettingKey, b)
	src.info = started(other(t, "DMR", ""), t0.Add(-45*time.Second))
	tr.Poll(context.Background())
	if len(src.tunes) != 1 || src.tunes[0] != (helper.DVTune{Mode: "P25", Tune: "10100"}) {
		t.Fatalf("%+v", src.tunes)
	}
}

func TestResumeFailureIsNotRetried(t *testing.T) {
	tr, src, _, _ := newTracker(t)
	tr.st = resumeState(true)
	tr.loaded = true
	src.terr = errors.New("no_info: Analog_Bridge is not running")
	var log strings.Builder
	tr.Logger = newLogger(&log)
	src.info = started(dmr(t, "9", "2", ""), t0.Add(-20*time.Second))
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
	src.info = started(dmr(t, "91", "2", "91"), t0.Add(-20*time.Second)) // the user tuned already
	tr.Poll(context.Background())
	tr.Poll(context.Background())
	if len(src.tunes) != 0 || strings.Count(log.String(), "skipped") != 1 {
		t.Fatalf("%d tunes, log %q", len(src.tunes), log.String())
	}
	if tr.st.LastResumeFor != src.info.Started {
		t.Fatal("start not marked")
	}
}

func newLogger(w *strings.Builder) *log.Logger { return log.New(w, "", 0) }
