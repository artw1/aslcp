package audio

import (
	"math"
	"testing"
	"time"
)

func sine(freq, amp float64, n int) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(math.Round(amp * math.Sin(2*math.Pi*freq*float64(i)/SampleRate)))
	}
	return out
}

func frameOf(s []int16) Frame { var f Frame; copy(f[:], s); return f }

// goertzel returns the power of freq in s, normalized so a full sine of
// amplitude A gives about A^2/4.
func goertzel(s []int16, freq float64) float64 {
	w := 2 * math.Pi * freq / SampleRate
	c := 2 * math.Cos(w)
	var s1, s2 float64
	for _, v := range s {
		s0 := float64(v) + c*s1 - s2
		s2, s1 = s1, s0
	}
	p := s1*s1 + s2*s2 - c*s1*s2
	n := float64(len(s))
	return p / (n * n)
}

func TestAGCConvergence(t *testing.T) {
	tests := []struct {
		name   string
		amp    float64 // peak, counts
		target float64
		max    float64
		wantDb float64 // expected output RMS
		tol    float64
	}{
		{"quiet to target", 400, -22, 30, -22, 1.5},
		{"loud pulled down", 20000, -22, 18, -22, 1.5},
		{"max gain limits", 300, -22, 6, -43.3 + 6, 1.5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAGC()
			p := AGCParams{TargetDb: tc.target, MaxGainDb: tc.max}
			var f Frame
			for i := 0; i < 300; i++ {
				f = frameOf(sine(500, tc.amp, FrameSamples))
				a.Process(&f, p)
			}
			got := Measure(&f).RMSDb
			if math.Abs(got-tc.wantDb) > tc.tol {
				t.Errorf("output %.1f dB, want %.1f", got, tc.wantDb)
			}
			if a.GainDb() > tc.max+0.01 {
				t.Errorf("gain %.1f exceeds max %.1f", a.GainDb(), tc.max)
			}
		})
	}
}

func TestAGCGateAndLimiter(t *testing.T) {
	a := NewAGC()
	var f Frame
	// Very quiet noise must not be pumped up.
	for i := 0; i < 200; i++ {
		f = frameOf(sine(300, 5, FrameSamples))
		a.Process(&f, DefaultAGCParams())
	}
	if a.GainDb() > 0.01 {
		t.Errorf("noise raised gain to %.2f dB", a.GainDb())
	}
	// A hot signal never passes the ceiling.
	for i := 0; i < 20; i++ {
		f = frameOf(sine(500, 32767, FrameSamples))
		a.Process(&f, DefaultAGCParams())
		if pk := Measure(&f).PeakDb; pk > 20*math.Log10(0.99) {
			t.Fatalf("peak %.2f dB too hot", pk)
		}
	}
}

func TestAGCParamsClamp(t *testing.T) {
	p := AGCParams{TargetDb: -60, MaxGainDb: 99}.Clamp()
	if p.TargetDb != MinTargetDb || p.MaxGainDb != MaxGainLimitDb {
		t.Errorf("clamp = %+v", p)
	}
}

func TestMixer(t *testing.T) {
	m := NewMixer()
	names := []string{"rf", "allstar", "echolink", "dv", "id"}
	ins := map[string]*Input{}
	for _, n := range names {
		in, err := m.AddInput(n, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		ins[n] = in
	}
	if _, err := m.AddInput("rf", 0, 0); err == nil {
		t.Error("duplicate input accepted")
	}
	for _, n := range names[:4] {
		excl := []string{}
		if n != "rf" {
			excl = []string{"id"}
		}
		if err := m.AddOutput(n, 0, excl...); err != nil {
			t.Fatal(err)
		}
	}
	ins["rf"].Push(frameOf(sine(500, 1000, FrameSamples)))
	ins["id"].Push(frameOf(sine(900, 1000, FrameSamples)))
	out := m.Mix()
	rf, as := out["rf"], out["allstar"]
	if p := goertzel(rf[:], 500); p > 1 {
		t.Errorf("rf heard itself: %v", p)
	}
	if p := goertzel(rf[:], 900); p < 1e4 {
		t.Errorf("rf missing id tone: %v", p)
	}
	if p := goertzel(as[:], 500); p < 1e4 {
		t.Errorf("allstar missing rf: %v", p)
	}
	if p := goertzel(as[:], 900); p > 1 {
		t.Errorf("allstar heard the id: %v", p)
	}
	act := m.ActiveInputs()
	if len(act) != 2 || !ins["rf"].Active() || ins["dv"].Active() {
		t.Errorf("active = %v", act)
	}
	m.Mix()
	if ins["rf"].Active() {
		t.Error("rf still active with no frames and no hang")
	}
}

func TestMixerGainClipAndHang(t *testing.T) {
	m := NewMixer()
	a, _ := m.AddInput("a", 0, 2)
	b, _ := m.AddInput("b", 6, 0)
	m.AddOutput("x", 0)
	var loud Frame
	for i := range loud {
		loud[i] = 30000
	}
	a.Push(loud)
	b.Push(loud)
	f := m.Mix()["x"]
	for _, s := range f {
		if s < 31000 {
			t.Fatalf("sample %d wrapped or collapsed", s)
		}
	}
	if !a.Active() {
		t.Fatal("a inactive")
	}
	m.Mix()
	m.Mix()
	if !a.Active() {
		t.Error("hang ended early")
	}
	m.Mix()
	if a.Active() {
		t.Error("hang did not end")
	}
	// -6 dB gain halves a small signal.
	a.SetGainDb(-6.0206)
	a.Push(frameOf(sine(500, 1000, FrameSamples)))
	g := m.Mix()["x"]
	if pk := Measure(&g).PeakDb; math.Abs(pk-20*math.Log10(500.0/32768)) > 0.2 {
		t.Errorf("peak %.2f dB after gain", pk)
	}
}

func TestToneFrequency(t *testing.T) {
	s := Drain(NewToneSeq(-10, Segment{1000, ms(200)}))
	if len(s) != 1600 {
		t.Fatalf("len %d", len(s))
	}
	mid := s[400:1200]
	on := goertzel(mid, 1000)
	for _, other := range []float64{700, 1300, 2000} {
		if p := goertzel(mid, other); p > on/100 {
			t.Errorf("%v Hz leakage %v vs %v", other, p, on)
		}
	}
	want := math.Pow(DBFS(-10), 2) / 4
	if math.Abs(on-want)/want > 0.05 {
		t.Errorf("power %v, want %v", on, want)
	}
}

func TestCWTiming(t *testing.T) {
	els, err := CWElements("PARIS", 20)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, e := range els {
		total += e.Samples
	}
	// PARIS is 43 units without the trailing word gap; a unit is 60 ms at 20 WPM.
	if want := 43 * ms(60); total != want {
		t.Errorf("PARIS = %d samples, want %d", total, want)
	}
	if els[0].Samples != ms(60) || !els[0].On || els[1].Samples != ms(60) || els[1].On || els[2].Samples != ms(180) {
		t.Errorf("bad start of P: %v", els[:3])
	}
	// "PARIS PARIS" with the word gap is 43+7+43 units.
	two, _ := CWElements("PARIS PARIS", 20)
	n := 0
	for _, e := range two {
		n += e.Samples
	}
	if n != 93*ms(60) {
		t.Errorf("two words = %d", n)
	}
	if _, err := CWElements("N0CALL~", 20); err == nil {
		t.Error("bad character accepted")
	}
}

func TestCWRender(t *testing.T) {
	c, err := NewCW("N0CALL", CWOptions{})
	if err != nil {
		t.Fatal(err)
	}
	s := Drain(c)
	if len(s) < c.Samples() || len(s) >= c.Samples()+FrameSamples {
		t.Errorf("rendered %d, message %d", len(s), c.Samples())
	}
	// Starts with N = dash dot: 180 samples-ms of tone at 800 Hz, -18 dBFS peak.
	dash := s[100 : ms(180)-100]
	if p, want := goertzel(dash, 800), math.Pow(DBFS(-18), 2)/4; math.Abs(p-want)/want > 0.1 {
		t.Errorf("CW power %v want %v", p, want)
	}
	if p := goertzel(dash, 1000); p > goertzel(dash, 800)/100 {
		t.Error("wrong pitch")
	}
	gap := s[ms(180)+40 : ms(180)+ms(60)-40]
	gf := frameOf(gap)
	if Measure(&gf).PeakDb > -80 {
		t.Error("element gap not silent")
	}
}

func TestWAV(t *testing.T) {
	src := sine(440, 8000, 400)
	b := EncodeWAV(src)
	got, err := ParseWAV(b)
	if err != nil || len(got) != 400 || got[17] != src[17] {
		t.Fatalf("round trip: %v len %d", err, len(got))
	}
	// Extra chunk before data, odd size, plus truncated data.
	extra := append([]byte{}, b[:36]...)
	extra = append(extra, 'L', 'I', 'S', 'T', 3, 0, 0, 0, 1, 2, 3, 0)
	extra = append(extra, b[36:len(b)-1]...)
	got, err = ParseWAV(extra)
	if err != nil || len(got) != 399 {
		t.Errorf("chunks: %v %d", err, len(got))
	}
	bad := EncodeWAV(src)
	bad[24] = 0x44 // 44100-ish rate
	bad[25] = 0xAC
	tests := map[string][]byte{"short": b[:8], "rate": bad, "nodata": b[:36], "magic": append([]byte("XXXX"), b[4:]...)}
	for name, in := range tests {
		if _, err := ParseWAV(in); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	p := NewWAVPlayer(src)
	var f Frame
	n := 0
	var last Frame
	for p.Next(&f) {
		n++
		last = f
	}
	if n != 3 || last[79] != src[399] || last[80] != 0 {
		t.Errorf("player frames %d", n)
	}
}

func TestMeterAndClock(t *testing.T) {
	var m Meter
	t0 := time.Unix(0, 0)
	f := frameOf(sine(500, 16384, FrameSamples))
	m.Observe(t0, &f)
	r := m.Snapshot(t0)
	if !r.Active || math.Abs(r.PeakDb+6) > 0.1 {
		t.Errorf("reading %+v", r)
	}
	r = m.Snapshot(t0.Add(time.Second))
	if r.Active || r.PeakDb > -20 {
		t.Errorf("no decay: %+v", r)
	}
	c := NewFakeClock(time.Time{})
	start := c.Now()
	done := make(chan time.Time)
	go func() { done <- <-c.C() }()
	c.Tick()
	if got := <-done; got.Sub(start) != FrameDuration || !c.Now().Equal(got) {
		t.Errorf("tick %v", got)
	}
}
