package api_test

import (
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
)

func TestDVAudioSettings(t *testing.T) {
	e, fe, fc := dvEnv(t, ctl.ModeLoop)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })

	type audio struct {
		AGCTx, AGCRx              bool
		AGCTargetDb, AGCMaxGainDb int
		GainRxDb, GainTxDb        int
		Applied                   bool
		ApplyError                string
	}
	var a audio
	if c := e.do("admin", "GET", "/api/dv/audio", nil, &a); c != 200 || a.AGCTx || a.AGCRx || a.AGCTargetDb != -22 || a.AGCMaxGainDb != 18 || a.GainRxDb != 0 || a.GainTxDb != 0 {
		t.Fatalf("defaults: %d %+v", c, a)
	}
	var nets struct{ Audio audio }
	if c := e.do("admin", "GET", "/api/dv/networks", nil, &nets); c != 200 || nets.Audio.AGCTargetDb != -22 {
		t.Fatalf("networks: %d %+v", c, nets.Audio)
	}
	body := map[string]any{"agcTx": true, "agcRx": false, "agcTargetDb": -20, "agcMaxGainDb": 10, "gainRxDb": 2, "gainTxDb": -3}
	if c := e.do("admin", "PUT", "/api/dv/audio", body, &a); c != 200 || !a.AGCTx || a.AGCTargetDb != -20 || a.AGCMaxGainDb != 10 || a.GainRxDb != 2 || a.GainTxDb != -3 {
		t.Fatalf("put: %d %+v", c, a)
	}
	if fe.audio == nil || fe.audio.AGCTargetDb != -20 {
		t.Fatalf("the helper got %+v", fe.audio)
	}
	reloaded := false
	for _, r := range fc.Requests() {
		if r.Op == ctl.OpReload {
			reloaded = true
		}
	}
	if !reloaded || !a.Applied {
		t.Fatalf("the engine was not reloaded: applied=%v %v", a.Applied, fc.Requests())
	}
	// a field left out keeps its default; bad values are refused and change nothing
	if c := e.do("admin", "PUT", "/api/dv/audio", map[string]any{"agcRx": true}, &a); c != 200 || !a.AGCRx || a.AGCTx || a.AGCTargetDb != -22 || a.AGCMaxGainDb != 18 {
		t.Fatalf("partial put: %d %+v", c, a)
	}
	for _, b := range []map[string]any{
		{"agcTargetDb": -5}, {"agcTargetDb": -41}, {"agcMaxGainDb": 31}, {"agcMaxGainDb": -1}, {"gainRxDb": 21}, {"gainTxDb": -21},
	} {
		expectErr(t, e, "admin", "PUT", "/api/dv/audio", b, 422, "invalid_field")
	}
	if fe.audio.AGCTargetDb != -22 || !fe.audio.AGCRx {
		t.Fatalf("a refused change was stored: %+v", fe.audio)
	}
	// admin only
	expectErr(t, e, "op", "PUT", "/api/dv/audio", body, 403, "forbidden")
	expectErr(t, e, "op", "GET", "/api/dv/audio", nil, 403, "forbidden")
}

func TestDVMeter(t *testing.T) {
	e, _, fc := dvEnv(t, ctl.ModeLoop)
	waitDV(t, e, func(s dvSnap) bool { return s.Running })
	st := ctl.Status{Version: "2.0.0-test", Started: time.Now(), Mode: ctl.ModeLoop, Vocoder: ctl.VocoderStatus{OK: true},
		Audio: &ctl.AudioStatus{TargetDb: -22,
			Tx: ctl.AudioDir{AGC: true, Active: true, PeakDb: -10.5, RmsDb: -20, OutPeakDb: -6, OutRmsDb: -16,
				Last: &ctl.AudioSummary{SpeechDb: -28, OutSpeechDb: -22, PeakDb: -4, Verdict: ctl.VerdictOK, AGC: true}},
			Rx: ctl.AudioDir{PeakDb: -90, RmsDb: -90, OutPeakDb: -90, OutRmsDb: -90}}}
	fc.SetStatus(st)
	type meter struct {
		Running bool
		Audio   *struct {
			TargetDb int
			Tx, Rx   struct {
				AGC, Active bool
				PeakDb      float64
				Last        *struct {
					SpeechDb float64
					Verdict  string
				}
			}
		}
	}
	// every role may read it
	for _, role := range []string{"viewer", "op", "admin"} {
		var m meter
		if c := e.do(role, "GET", "/api/dv/meter", nil, &m); c != 200 {
			t.Fatalf("%s: %d", role, c)
		}
		if !m.Running || m.Audio == nil || m.Audio.TargetDb != -22 || !m.Audio.Tx.Active || m.Audio.Tx.PeakDb != -10.5 || m.Audio.Tx.Last == nil || m.Audio.Tx.Last.Verdict != "ok" || m.Audio.Rx.Last != nil {
			t.Fatalf("%s: %+v", role, m)
		}
	}
}
