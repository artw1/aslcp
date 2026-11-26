package alsa

import "math"

// The radio sound chip runs at 48000 Hz and the node at 8000 Hz, a factor of
// six. Both directions use one linear-phase low-pass FIR (Kaiser window,
// about 70 dB stopband, passband flat to 3.4 kHz) applied as a polyphase
// structure.
const (
	hwRate      = 48000
	resFactor   = hwRate / SampleRate // 6
	resPhaseLen = 33                  // taps per polyphase branch
	resTaps     = resFactor*resPhaseLen - 5
	resCutoff   = 3900.0
	resBeta     = 7.0
)

// resCoef holds the prototype filter, DC gain 1, zero padded to a whole
// number of polyphase branches.
var resCoef = designFilter()

func designFilter() []float64 {
	n := resFactor * resPhaseLen
	h := make([]float64, n)
	m := float64(resTaps-1) / 2
	fc := resCutoff / hwRate // cycles per sample
	var sum float64
	for i := 0; i < resTaps; i++ {
		x := float64(i) - m
		sinc := 2 * fc
		if x != 0 {
			sinc = math.Sin(2*math.Pi*fc*x) / (math.Pi * x)
		}
		r := x / m
		w := bessel0(resBeta*math.Sqrt(1-r*r)) / bessel0(resBeta)
		h[i] = sinc * w
		sum += h[i]
	}
	for i := range h {
		h[i] /= sum
	}
	return h
}

// bessel0 is the zeroth-order modified Bessel function of the first kind.
func bessel0(x float64) float64 {
	sum, term := 1.0, 1.0
	for k := 1; k < 50; k++ {
		term *= (x / (2 * float64(k))) * (x / (2 * float64(k)))
		sum += term
		if term < 1e-12*sum {
			break
		}
	}
	return sum
}

func clip16(v float64) int16 {
	v = math.Round(v)
	if v > math.MaxInt16 {
		return math.MaxInt16
	}
	if v < math.MinInt16 {
		return math.MinInt16
	}
	return int16(v)
}

// Down converts 48 kHz samples to 8 kHz. It keeps filter state between
// calls, so input may arrive in any chunk size.
type Down struct {
	hist []float64 // last len(resCoef)-1 input samples
	next int       // index into the next input chunk of the next output
}

// NewDown returns a 48 kHz to 8 kHz converter.
func NewDown() *Down { return &Down{hist: make([]float64, len(resCoef)-1)} }

// Process appends the 8 kHz output for in to out and returns it.
func (d *Down) Process(in []int16, out []int16) []int16 {
	t := len(resCoef)
	buf := make([]float64, 0, len(d.hist)+len(in))
	buf = append(buf, d.hist...)
	for _, s := range in {
		buf = append(buf, float64(s))
	}
	j := d.next
	for ; j < len(in); j += resFactor {
		// Output sample for input index j: sum h[k] * x[j-k].
		base := t - 1 + j
		var acc float64
		for k := 0; k < t; k++ {
			acc += resCoef[k] * buf[base-k]
		}
		out = append(out, clip16(acc))
	}
	d.next = j - len(in)
	copy(d.hist, buf[len(buf)-len(d.hist):])
	return out
}

// Up converts 8 kHz samples to 48 kHz.
type Up struct {
	hist [resPhaseLen - 1]float64 // previous input samples, newest last
}

// NewUp returns an 8 kHz to 48 kHz converter.
func NewUp() *Up { return &Up{} }

// Process appends resFactor output samples per input sample to out.
func (u *Up) Process(in []int16, out []int16) []int16 {
	hl := len(u.hist)
	buf := make([]float64, 0, hl+len(in))
	buf = append(buf, u.hist[:]...)
	for _, s := range in {
		buf = append(buf, float64(s))
	}
	for i := range in {
		base := hl + i
		for p := 0; p < resFactor; p++ {
			var acc float64
			for j := 0; j < resPhaseLen; j++ {
				acc += resCoef[resFactor*j+p] * buf[base-j]
			}
			out = append(out, clip16(acc*resFactor))
		}
	}
	copy(u.hist[:], buf[len(buf)-hl:])
	return out
}
