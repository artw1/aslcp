package audio

import (
	"math"
	"testing"
	"time"
)

// sineFrames returns n frames of a 500 Hz sine with the given peak
// amplitude (1 = full scale).
func sineFrames(n int, amp float64) []Frame {
	out := make([]Frame, n)
	k := 0
	for i := range out {
		for j := range out[i] {
			out[i][j] = int16(math.Round(amp * 32767 * math.Sin(2*math.Pi*500*float64(k)/SampleRate)))
			k++
		}
	}
	return out
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.2f, want %.2f +/- %.2f", what, got, want, tol)
	}
}

func TestAnalyzeFullScaleSine(t *testing.T) {
	r := Analyze(time.Time{}, sineFrames(50, 1))
	near(t, "peak", r.PeakDb, 0, 0.01)
	near(t, "rms", r.RMSDb, -3.01, 0.05)
	if r.DurationMs != 1000 || r.Frames != 50 || r.VoicedFrames != 50 {
		t.Errorf("report %+v", r)
	}
	if r.Clipped == 0 || len(r.Warnings) != 1 || r.Warnings[0] != WarnClipping {
		t.Errorf("clipped %d warnings %v", r.Clipped, r.Warnings)
	}
}

func TestAnalyzeQuietSine(t *testing.T) {
	r := Analyze(time.Time{}, sineFrames(50, 0.1)) // -20 dBFS peak
	near(t, "peak", r.PeakDb, -20, 0.05)
	near(t, "rms", r.RMSDb, -23.01, 0.05)
	if r.Clipped != 0 || len(r.Warnings) != 0 {
		t.Errorf("report %+v", r)
	}
	q := Analyze(time.Time{}, sineFrames(50, 0.01)) // -40 dBFS peak
	if len(q.Warnings) != 1 || q.Warnings[0] != WarnTooQuiet {
		t.Errorf("warnings %v", q.Warnings)
	}
}

func TestAnalyzeSilence(t *testing.T) {
	r := Analyze(time.Time{}, make([]Frame, 10))
	if r.PeakDb != FloorDb || r.RMSDb != FloorDb || r.VoicedFrames != 0 {
		t.Errorf("report %+v", r)
	}
	if len(r.Warnings) != 1 || r.Warnings[0] != WarnTooQuiet {
		t.Errorf("warnings %v", r.Warnings)
	}
	if e := Analyze(time.Time{}, nil); len(e.Warnings) != 0 || e.Frames != 0 {
		t.Errorf("empty report %+v", e)
	}
}

// Pauses below the voiced floor do not pull the RMS down.
func TestAnalyzeIgnoresPauses(t *testing.T) {
	frames := append(sineFrames(10, 0.1), make([]Frame, 40)...)
	r := Analyze(time.Time{}, frames)
	near(t, "rms", r.RMSDb, -23.01, 0.05)
	if r.VoicedFrames != 10 || r.Frames != 50 {
		t.Errorf("report %+v", r)
	}
}

func TestAnalyzeNearFullScalePeakWarns(t *testing.T) {
	// A peak at -0.45 dBFS has no sample at full scale but still warns.
	r := Analyze(time.Time{}, sineFrames(10, 0.95))
	if r.Clipped != 0 || len(r.Warnings) != 1 || r.Warnings[0] != WarnClipping {
		t.Errorf("report %+v", r)
	}
	if q := Analyze(time.Time{}, sineFrames(10, 0.9)); len(q.Warnings) != 0 { // -0.9 dBFS
		t.Errorf("warnings %v", q.Warnings)
	}
}

func TestRolling(t *testing.T) {
	var r Rolling
	if p, m := r.Levels(); p != FloorDb || m != FloorDb {
		t.Fatalf("empty %v %v", p, m)
	}
	loud, quiet := sineFrames(1, 1)[0], sineFrames(1, 0.1)[0]
	r.Add(&loud)
	for i := 0; i < RollingFrames; i++ {
		r.Add(&quiet)
	}
	p, m := r.Levels() // the loud frame has left the window
	near(t, "peak", p, -20, 0.1)
	near(t, "rms", m, -23.01, 0.1)
}
