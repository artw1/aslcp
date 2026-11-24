package bridge

import (
	"math"

	"github.com/artw1/aslcp/internal/dv/vocoder"
)

// A feed-forward automatic gain control for one direction of the bridge.
// It works on one 160-sample frame (20 ms at 8 kHz) at a time and adds no
// latency: the gain for a frame is decided from that frame itself, and the
// frame is ramped to it sample by sample. A speech gate keeps silence and
// noise from being pumped up; a peak limiter and a soft knee keep the result
// below full scale.

const (
	frameSec = float64(vocoder.PCMSamples) / 8000 // 0.02

	agcAttackSec  = 0.010 // gain going down
	agcReleaseSec = 0.400 // gain going up
	agcMinGainDb  = -20.0 // lowest gain
	agcCeiling    = 0.92  // limiter ceiling, fraction of full scale
	agcKnee       = 0.85  // the soft clip starts here

	gateMinDb     = -50.0 // the gate never opens below this level
	gateAboveDb   = 10.0  // gate = noise floor + this
	gateHangSec   = 0.25  // the gate holds this long after speech
	gateFloorInit = -80.0
	gateFloorMax  = -55.0 // the tracked noise floor never goes above this
	gateRiseDbSec = 1.5   // how fast the noise floor may rise
	silenceDb     = -90.0 // the level reported for silence

	// A sample at or above this counts as clipped (about -0.2 dBFS).
	clipLevel = 32000
)

// AGCParams are the settings read on every frame (they follow a hot reload).
type AGCParams struct {
	TargetDb  float64 // speech level to aim for, dBFS RMS
	MaxGainDb float64 // highest gain
}

// gateState is what the speech gate says about one frame.
type gateState int

const (
	gateClosed gateState = iota // below the gate: noise or silence
	gateHold                    // below the gate, but within the hang time of speech
	gateOpen                    // above the gate: speech
)

// gate is the speech detector: a slow noise-floor tracker with the gate 10 dB
// above it (and never below -50 dBFS) and a hang time.
type gate struct {
	floor float64
	hang  int
}

func (g *gate) reset() { g.floor, g.hang = gateFloorInit, 0 }

// update takes the level of one frame in dBFS (RMS).
func (g *gate) update(levelDb float64) gateState {
	if levelDb < g.floor {
		g.floor = math.Max(levelDb, silenceDb)
	} else {
		g.floor = math.Min(g.floor+gateRiseDbSec*frameSec, gateFloorMax)
	}
	if levelDb > math.Max(g.floor+gateAboveDb, gateMinDb) {
		g.hang = int(math.Round(gateHangSec / frameSec))
		return gateOpen
	}
	if g.hang > 0 {
		g.hang--
		return gateHold
	}
	return gateClosed
}

// AGC is one direction's gain control. It is not safe for concurrent use; the
// router calls it from its own goroutine only.
type AGC struct {
	gainDb float64
	g      gate
}

// NewAGC returns an AGC at 0 dB.
func NewAGC() *AGC { a := &AGC{}; a.Reset(); return a }

// Reset starts a new call: gain 0 dB and the noise floor forgotten.
func (a *AGC) Reset() { a.gainDb = 0; a.g.reset() }

// GainDb is the gain at the end of the last frame.
func (a *AGC) GainDb() float64 { return a.gainDb }

func dbToLin(db float64) float32 { return float32(math.Pow(10, db/20)) }

func linToDb(v float64) float64 {
	if v <= 0 {
		return silenceDb
	}
	return math.Max(20*math.Log10(v), silenceDb)
}

// softClip is linear up to the knee and bends smoothly towards, but never
// reaches, 1.
func softClip(x float32) float32 {
	const k = float32(agcKnee)
	ax := x
	if ax < 0 {
		ax = -ax
	}
	if ax <= k {
		return x
	}
	y := k + (1-k)*float32(math.Tanh(float64((ax-k)/(1-k))))
	if x < 0 {
		return -y
	}
	return y
}

// Process applies the AGC to pcm in place.
func (a *AGC) Process(pcm *[vocoder.PCMSamples]int16, p AGCParams) {
	var in [vocoder.PCMSamples]float32
	var sum float64
	var peak float32
	for i, s := range pcm {
		v := float32(s) / 32768
		in[i] = v
		sum += float64(v) * float64(v)
		if av := float32(math.Abs(float64(v))); av > peak {
			peak = av
		}
	}
	levelDb := linToDb(math.Sqrt(sum / vocoder.PCMSamples))
	st := a.g.update(levelDb)

	g0 := a.gainDb
	g1 := g0
	if st != gateClosed {
		desired := math.Min(math.Max(p.TargetDb-levelDb, agcMinGainDb), p.MaxGainDb)
		if st == gateHold && desired > g0 {
			desired = g0 // after speech the gain may fall but not rise
		}
		tc := agcReleaseSec
		if desired < g0 {
			tc = agcAttackSec
		}
		g1 = g0 + (1-math.Exp(-frameSec/tc))*(desired-g0)
	}
	// Peak limiter: no sample of this frame may pass the ceiling, at the start
	// of the ramp or at its end.
	if peak > 0 {
		lim := linToDb(agcCeiling / float64(peak))
		g0, g1 = math.Min(g0, lim), math.Min(g1, lim)
	}
	a.gainDb = g1

	lin0, lin1 := dbToLin(g0), dbToLin(g1)
	step := (lin1 - lin0) / vocoder.PCMSamples
	g := lin0
	for i, v := range in {
		g += step
		y := softClip(v * g)
		o := float32(math.Round(float64(y * 32768)))
		if o > math.MaxInt16 {
			o = math.MaxInt16
		} else if o < math.MinInt16 {
			o = math.MinInt16
		}
		pcm[i] = int16(o)
	}
}
