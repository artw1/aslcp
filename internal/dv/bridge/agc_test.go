package bridge

import (
	"math"
	"math/rand"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

type frame = [vocoder.PCMSamples]int16

// sineFrames returns n frames of a 440 Hz sine whose RMS is rmsDb dBFS.
func sineFrames(n int, rmsDb float64) []frame {
	amp := math.Pow(10, rmsDb/20) * math.Sqrt2 * 32768
	out := make([]frame, n)
	k := 0
	for i := range out {
		for j := range out[i] {
			v := math.Round(amp * math.Sin(2*math.Pi*440*float64(k)/8000))
			out[i][j] = int16(math.Max(math.Min(v, 32767), -32768))
			k++
		}
	}
	return out
}

func noiseFrames(n int, rmsDb float64) []frame {
	rng := rand.New(rand.NewSource(7))
	amp := math.Pow(10, rmsDb/20) * 32768
	out := make([]frame, n)
	for i := range out {
		for j := range out[i] {
			out[i][j] = int16(rng.NormFloat64() * amp)
		}
	}
	return out
}

func rmsDbOf(f *frame) float64 { return measure(f).rmsDb }

var testParams = AGCParams{TargetDb: -22, MaxGainDb: 18}

func TestAGCQuietSineConvergesToTarget(t *testing.T) {
	a := NewAGC()
	fs := sineFrames(300, -40) // 6 s
	var out float64
	for i := range fs {
		a.Process(&fs[i], testParams)
		if a.GainDb() > testParams.MaxGainDb+0.01 {
			t.Fatalf("frame %d: gain %.2f dB over the maximum", i, a.GainDb())
		}
		out = rmsDbOf(&fs[i])
	}
	if math.Abs(out-(-22)) > 2 {
		t.Fatalf("settled at %.2f dBFS, want -22 +-2", out)
	}
}

func TestAGCRespectsLowMaxGain(t *testing.T) {
	a := NewAGC()
	p := AGCParams{TargetDb: -22, MaxGainDb: 6}
	fs := sineFrames(300, -40)
	for i := range fs {
		a.Process(&fs[i], p)
	}
	if g := a.GainDb(); g > 6.01 || g < 5.5 {
		t.Fatalf("gain %.2f dB, want the 6 dB maximum", g)
	}
}

func TestAGCLoudSineIsPulledDown(t *testing.T) {
	a := NewAGC()
	fs := sineFrames(200, -6)
	for i := range fs {
		a.Process(&fs[i], testParams)
	}
	if out := rmsDbOf(&fs[len(fs)-1]); math.Abs(out-(-22)) > 2 {
		t.Fatalf("settled at %.2f dBFS, want -22 +-2", out)
	}
	if a.GainDb() < agcMinGainDb-0.01 {
		t.Fatalf("gain %.2f below the floor", a.GainDb())
	}
}

func TestAGCLimiterCeiling(t *testing.T) {
	const ceil = agcCeiling * 32768
	// a near-full-scale sine (peak -0.1 dBFS) and a full-scale square wave
	loud := sineFrames(100, -3.2)
	sq := make([]frame, 50)
	for i := range sq {
		for j := range sq[i] {
			if (j/10)%2 == 0 {
				sq[i][j] = 32767
			} else {
				sq[i][j] = -32768
			}
		}
	}
	for name, fs := range map[string][]frame{"sine": loud, "square": sq} {
		a := NewAGC()
		for i := range fs {
			a.Process(&fs[i], testParams)
			for j, v := range fs[i] {
				if math.Abs(float64(v)) > ceil+1 {
					t.Fatalf("%s frame %d sample %d: %d over the ceiling %.0f", name, i, j, v, ceil)
				}
			}
		}
	}
}

func TestAGCSilenceAndNoiseGetNoGain(t *testing.T) {
	for name, fs := range map[string][]frame{"silence": make([]frame, 200), "noise -60": noiseFrames(400, -60)} {
		a := NewAGC()
		for i := range fs {
			in := fs[i]
			a.Process(&fs[i], testParams)
			if a.GainDb() > 0 {
				t.Fatalf("%s frame %d: gain rose to %.2f dB", name, i, a.GainDb())
			}
			if fs[i] != in {
				t.Fatalf("%s frame %d: audio changed", name, i)
			}
		}
	}
}

func TestAGCQuietToLoudStepDoesNotOvershoot(t *testing.T) {
	const ceil = agcCeiling * 32768
	a := NewAGC()
	fs := append(sineFrames(150, -40), sineFrames(60, -2)...)
	for i := range fs {
		a.Process(&fs[i], testParams)
		if i < 150 {
			continue
		}
		for j, v := range fs[i] {
			if math.Abs(float64(v)) > ceil+1 {
				t.Fatalf("step frame %d sample %d: %d over the ceiling", i-150, j, v)
			}
		}
	}
	// the AGC had raised the gain to 18 dB; the first loud frame must have taken it down at once
	if a.GainDb() > -1 {
		t.Fatalf("gain after the step %.2f dB, want it pulled below -1", a.GainDb())
	}
}

func TestAGCHoldsGainInPausesAndNeverRisesThere(t *testing.T) {
	a := NewAGC()
	for _, f := range sineFrames(100, -40) {
		f := f
		a.Process(&f, testParams)
	}
	g := a.GainDb()
	// 1 s of room noise afterwards: the gain stays where it is
	for _, f := range noiseFrames(50, -62) {
		f := f
		a.Process(&f, testParams)
		if a.GainDb() > g+1e-9 {
			t.Fatalf("gain rose in a pause: %.2f -> %.2f dB", g, a.GainDb())
		}
	}
}

func TestAGCReset(t *testing.T) {
	a := NewAGC()
	for _, f := range sineFrames(100, -40) {
		f := f
		a.Process(&f, testParams)
	}
	if a.GainDb() < 5 {
		t.Fatalf("test set-up: gain only %.2f dB", a.GainDb())
	}
	a.Reset()
	if a.GainDb() != 0 {
		t.Fatalf("after Reset the gain is %.2f dB", a.GainDb())
	}
	// and the first frame after it starts from 0 dB (no ramp from the old gain)
	f := sineFrames(1, -40)[0]
	in := f
	a.Process(&f, testParams)
	if d := rmsDbOf(&f) - rmsDbOf(&in); d < 0 || d > 1.5 {
		t.Fatalf("first frame after Reset changed by %.2f dB", d)
	}
}

func TestAGCUnityIsBitExact(t *testing.T) {
	a := NewAGC()
	fs := noiseFrames(20, -70)
	for i := range fs {
		in := fs[i]
		a.Process(&fs[i], testParams)
		if fs[i] != in {
			t.Fatal("a frame at 0 dB was changed")
		}
	}
}

func BenchmarkAGC(b *testing.B) {
	a := NewAGC()
	f := sineFrames(1, -30)[0]
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		g := f
		a.Process(&g, testParams)
	}
}

// ---- meter and summary ----

func TestVerdict(t *testing.T) {
	for _, c := range []struct {
		speech  float64
		clipped int
		target  int
		want    string
	}{
		{-33, 0, -22, ctl.VerdictLow},
		{-32, 0, -22, ctl.VerdictOK}, // exactly target-10 is ok
		{-22, 0, -22, ctl.VerdictOK},
		{-14, 0, -22, ctl.VerdictOK}, // exactly target+8 is ok
		{-13.9, 0, -22, ctl.VerdictHigh},
		{-22, 1, -22, ctl.VerdictHigh}, // clipping is always high
		{-40, 3, -22, ctl.VerdictHigh},
		{-45, 0, -30, ctl.VerdictLow}, // follows the configured target
		{-45, 0, -40, ctl.VerdictOK},
	} {
		if got := Verdict(c.speech, c.clipped, c.target); got != c.want {
			t.Errorf("Verdict(%v, %d, %d) = %s, want %s", c.speech, c.clipped, c.target, got, c.want)
		}
	}
}

func TestMeterSummaryBeforeAndAfterAGC(t *testing.T) {
	p := newAudioPath()
	p.begin()
	now := time.Unix(1000, 0)
	for _, f := range sineFrames(300, -40) {
		f := f
		p.process(now, &f, true, testParams)
		now = now.Add(20 * time.Millisecond)
	}
	p.end(now, 6*time.Second, -22)
	s := p.m.snapshot(now, true).Last
	if s == nil {
		t.Fatal("no summary")
	}
	if math.Abs(s.SpeechDb-(-40)) > 0.2 {
		t.Errorf("speech before AGC %.1f, want -40", s.SpeechDb)
	}
	if s.OutSpeechDb < -30 || s.OutSpeechDb > -21 {
		t.Errorf("speech after AGC %.1f, want it raised towards -22", s.OutSpeechDb)
	}
	if s.PeakDb > -36 || s.PeakDb < -37.5 || s.OutPeakDb < s.PeakDb {
		t.Errorf("peaks %.1f / %.1f", s.PeakDb, s.OutPeakDb)
	}
	if s.Clipped != 0 || s.DurationMs != 6000 || !s.AGC || s.Verdict != ctl.VerdictOK && s.Verdict != ctl.VerdictLow {
		t.Errorf("summary %+v", s)
	}
	if s.Verdict != ctl.VerdictLow { // -40 is 18 dB under -22
		t.Errorf("verdict %s, want low (judged before the AGC)", s.Verdict)
	}
}

func TestMeterAGCOffBeforeEqualsAfter(t *testing.T) {
	p := newAudioPath()
	p.begin()
	now := time.Unix(1000, 0)
	for _, f := range sineFrames(50, -20) {
		f := f
		p.process(now, &f, false, testParams)
		now = now.Add(20 * time.Millisecond)
	}
	p.end(now, time.Second, -22)
	s := p.m.snapshot(now, false).Last
	if s.SpeechDb != s.OutSpeechDb || s.PeakDb != s.OutPeakDb || s.AGC || s.Verdict != ctl.VerdictOK {
		t.Fatalf("summary %+v", s)
	}
}

func TestMeterClipCountAndNoSpeech(t *testing.T) {
	p := newAudioPath()
	p.begin()
	now := time.Unix(1000, 0)
	hot := sineFrames(10, -0.5) // peaks at full scale
	for i := range hot {
		p.process(now, &hot[i], false, testParams)
		now = now.Add(20 * time.Millisecond)
	}
	p.end(now, 200*time.Millisecond, -22)
	s := p.m.snapshot(now, false).Last
	if s.Clipped != 10 || s.Verdict != ctl.VerdictHigh {
		t.Fatalf("summary %+v", s)
	}
	// a call of room noise has no speech frames: low, and it replaces the old summary
	p.begin()
	for _, f := range noiseFrames(20, -70) {
		f := f
		p.process(now, &f, false, testParams)
	}
	p.end(now, time.Second, -22)
	if s := p.m.snapshot(now, false).Last; s.SpeechDb != silenceDb || s.Verdict != ctl.VerdictLow {
		t.Fatalf("noise-only summary %+v", s)
	}
	// a call without any frame leaves the previous summary
	p.begin()
	p.end(now, time.Second, -22)
	if s := p.m.snapshot(now, false).Last; s == nil || s.SpeechDb != silenceDb {
		t.Fatalf("an empty call changed the summary: %+v", s)
	}
}

func TestMeterLiveLevelsDecay(t *testing.T) {
	p := newAudioPath()
	p.begin()
	now := time.Unix(1000, 0)
	if d := p.m.snapshot(now, false); d.Active || d.PeakDb != silenceDb {
		t.Fatalf("idle meter %+v", d)
	}
	f := sineFrames(1, -20)[0]
	p.process(now, &f, false, testParams)
	d := p.m.snapshot(now, false)
	if !d.Active || math.Abs(d.RmsDb-(-20)) > 0.2 || d.PeakDb < -17.5 || d.PeakDb > -16.5 {
		t.Fatalf("live meter %+v", d)
	}
	d = p.m.snapshot(now.Add(time.Second), false)
	if d.Active || d.RmsDb > -39 || d.RmsDb < -41 {
		t.Fatalf("one second later %+v, want about 20 dB lower and inactive", d)
	}
	if d = p.m.snapshot(now.Add(time.Hour), false); d.PeakDb != silenceDb {
		t.Fatalf("an hour later %+v", d)
	}
}
