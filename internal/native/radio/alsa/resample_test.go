package alsa

import (
	"math"
	"testing"
)

func sine(freq, rate float64, n int, amp float64) []int16 {
	out := make([]int16, n)
	for i := range out {
		out[i] = int16(math.Round(amp * math.Sin(2*math.Pi*freq*float64(i)/rate)))
	}
	return out
}

// tone returns the amplitude of the component at freq (Goertzel-like
// correlation) over x, ignoring the first skip samples.
func tone(x []int16, freq, rate float64, skip int) float64 {
	var re, im float64
	n := 0
	for i := skip; i < len(x); i++ {
		w := 2 * math.Pi * freq * float64(i) / rate
		re += float64(x[i]) * math.Cos(w)
		im += float64(x[i]) * math.Sin(w)
		n++
	}
	return 2 * math.Hypot(re, im) / float64(n)
}

func db(r float64) float64 { return 20 * math.Log10(r) }

func TestFilterDCGain(t *testing.T) {
	var s float64
	for _, c := range resCoef {
		s += c
	}
	if math.Abs(s-1) > 1e-9 {
		t.Fatalf("DC gain = %v", s)
	}
	if len(resCoef) != resFactor*resPhaseLen {
		t.Fatalf("len = %d", len(resCoef))
	}
}

func TestDownFrequencyResponse(t *testing.T) {
	const amp = 10000.0
	tests := []struct {
		name     string
		freq     float64 // Hz at 48 kHz input
		alias    float64 // where it lands at 8 kHz
		minDB    float64
		maxDB    float64
		measured float64
	}{
		{"300 Hz", 300, 300, -0.3, 0.3, 0},
		{"1 kHz", 1000, 1000, -0.1, 0.1, 0},
		{"2 kHz", 2000, 2000, -0.2, 0.2, 0},
		{"3 kHz", 3000, 3000, -0.5, 0.2, 0},
		{"3.4 kHz", 3400, 3400, -1.5, 0.2, 0},
		{"5 kHz alias", 5000, 3000, math.Inf(-1), -60, 0},
		{"6 kHz alias", 6000, 2000, math.Inf(-1), -60, 0},
		{"7 kHz alias", 7000, 1000, math.Inf(-1), -60, 0},
		{"11 kHz alias", 11000, 3000, math.Inf(-1), -60, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := sine(tc.freq, 48000, 48000, amp)
			out := NewDown().Process(in, nil)
			if len(out) != 8000 {
				t.Fatalf("len(out) = %d, want 8000", len(out))
			}
			g := db(tone(out, tc.alias, 8000, 100) / amp)
			if g < tc.minDB || g > tc.maxDB {
				t.Errorf("gain = %.2f dB, want %.1f..%.1f", g, tc.minDB, tc.maxDB)
			}
		})
	}
}

func TestUpFrequencyResponse(t *testing.T) {
	const amp = 10000.0
	tests := []struct {
		name  string
		freq  float64
		image float64 // image frequency at 48 kHz, 0 for none
	}{
		{"500 Hz", 500, 7500},
		{"1 kHz", 1000, 7000},
		{"3 kHz", 3000, 5000},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			in := sine(tc.freq, 8000, 8000, amp)
			out := NewUp().Process(in, nil)
			if len(out) != 48000 {
				t.Fatalf("len(out) = %d", len(out))
			}
			g := db(tone(out, tc.freq, 48000, 600) / amp)
			if math.Abs(g) > 0.5 {
				t.Errorf("gain = %.2f dB", g)
			}
			img := db(tone(out, tc.image, 48000, 600) / amp)
			if img > -60 {
				t.Errorf("image at %.0f Hz = %.1f dB, want below -60", tc.image, img)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	// 8k -> 48k -> 8k keeps the signal with a constant delay of
	// (len(h)-1)/6 = 32 samples (two filters of 96 samples at 48 kHz).
	const amp = 12000.0
	for _, freq := range []float64{400, 1000, 2500} {
		in := sine(freq, 8000, 4000, amp)
		mid := NewUp().Process(in, nil)
		out := NewDown().Process(mid, nil)
		if len(out) != len(in) {
			t.Fatalf("len = %d", len(out))
		}
		const delay = 32
		var errSum, sigSum float64
		for i := 400; i < len(in)-delay; i++ {
			d := float64(out[i+delay]) - float64(in[i])
			errSum += d * d
			sigSum += float64(in[i]) * float64(in[i])
		}
		snr := 10 * math.Log10(sigSum/errSum)
		if snr < 50 {
			t.Errorf("%.0f Hz: round trip SNR = %.1f dB, want >= 50", freq, snr)
		}
	}
}

func TestChunkingIsTransparent(t *testing.T) {
	in := sine(1000, 48000, 9600, 8000)
	whole := NewDown().Process(in, nil)
	d := NewDown()
	var parts []int16
	// Chunk sizes that are not multiples of 6 exercise the phase carry.
	for i := 0; i < len(in); {
		n := min(7+i%13, len(in)-i)
		parts = d.Process(in[i:i+n], parts)
		i += n
	}
	if len(parts) != len(whole) {
		t.Fatalf("len = %d, want %d", len(parts), len(whole))
	}
	for i := range whole {
		if parts[i] != whole[i] {
			t.Fatalf("sample %d: %d != %d", i, parts[i], whole[i])
		}
	}
	u1 := NewUp().Process(sine(700, 8000, 400, 8000), nil)
	u := NewUp()
	var u2 []int16
	src := sine(700, 8000, 400, 8000)
	for i := 0; i < len(src); i += 37 {
		u2 = u.Process(src[i:min(i+37, len(src))], u2)
	}
	for i := range u1 {
		if u1[i] != u2[i] {
			t.Fatalf("up sample %d differs", i)
		}
	}
}

func TestClipping(t *testing.T) {
	tests := []struct {
		in   float64
		want int16
	}{{0, 0}, {1.4, 1}, {1.6, 2}, {40000, 32767}, {-40000, -32768}}
	for _, tc := range tests {
		if got := clip16(tc.in); got != tc.want {
			t.Errorf("clip16(%v) = %d, want %d", tc.in, got, tc.want)
		}
	}
}
