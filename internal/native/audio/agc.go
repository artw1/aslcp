package audio

import "math"

// The AGC is a faithful port, for 8 kHz Frames, of the DV module's AGC in
// internal/dv/bridge/agc.go (same constants, gate and limiter), so that every
// leg of the native node levels the way the DV legs do. It is feed-forward and
// adds no latency: the gain for a frame is decided from that frame and ramped
// sample by sample. A speech gate keeps silence and noise from being pumped up.

const (
	frameSec = float64(FrameSamples) / SampleRate // 0.02

	agcAttackSec  = 0.010 // gain going down
	agcReleaseSec = 0.400 // gain going up
	agcMinGainDb  = -20.0 // lowest gain
	agcCeiling    = 0.92  // limiter ceiling, fraction of full scale
	agcKnee       = 0.85  // the soft clip starts here

	gateMinDb     = -50.0
	gateAboveDb   = 10.0
	gateHangSec   = 0.25
	gateFloorInit = -80.0
	gateFloorMax  = -55.0
	gateRiseDbSec = 1.5
)

// AGC settings as in the DV module (internal/dv/ctl): target -40..-6 dB,
// default -22; maximum gain 0..30 dB, default 18.
const (
	DefaultTargetDb  = -22
	DefaultMaxGainDb = 18
	MinTargetDb      = -40
	MaxTargetDb      = -6
	MaxGainLimitDb   = 30
)

// AGCParams are the settings read on every frame.
type AGCParams struct {
	TargetDb  float64 // speech level to aim for, dBFS RMS
	MaxGainDb float64 // highest gain
}

// DefaultAGCParams returns the DV module's defaults (-22 dB target, 18 dB max).
func DefaultAGCParams() AGCParams {
	return AGCParams{TargetDb: DefaultTargetDb, MaxGainDb: DefaultMaxGainDb}
}

// Clamp returns p limited to the allowed ranges.
func (p AGCParams) Clamp() AGCParams {
	p.TargetDb = math.Min(math.Max(p.TargetDb, MinTargetDb), MaxTargetDb)
	p.MaxGainDb = math.Min(math.Max(p.MaxGainDb, 0), MaxGainLimitDb)
	return p
}

type gateState int

const (
	gateClosed gateState = iota
	gateHold
	gateOpen
)

// gate is the speech detector: a slow noise-floor tracker with the gate 10 dB
// above it (never below -50 dBFS) and a hang time.
type gate struct {
	floor float64
	hang  int
}

func (g *gate) reset() { g.floor, g.hang = gateFloorInit, 0 }

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

// AGC is one leg's gain control. It is not safe for concurrent use.
type AGC struct {
	gainDb float64
	g      gate
}

// NewAGC returns an AGC at 0 dB.
func NewAGC() *AGC { a := &AGC{}; a.Reset(); return a }

// Reset starts a new transmission: gain 0 dB and the noise floor forgotten.
func (a *AGC) Reset() { a.gainDb = 0; a.g.reset() }

// GainDb is the gain at the end of the last frame.
func (a *AGC) GainDb() float64 { return a.gainDb }

// Process applies the AGC to f in place.
func (a *AGC) Process(f *Frame, p AGCParams) {
	var in [FrameSamples]float64
	var sum, peak float64
	for i, s := range f {
		v := float64(s) / 32768
		in[i] = v
		sum += v * v
		if av := math.Abs(v); av > peak {
			peak = av
		}
	}
	levelDb := linToDb(math.Sqrt(sum / FrameSamples))
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
	// Peak limiter: no sample may pass the ceiling at either end of the ramp.
	if peak > 0 {
		lim := linToDb(agcCeiling / peak)
		g0, g1 = math.Min(g0, lim), math.Min(g1, lim)
	}
	a.gainDb = g1

	lin0, lin1 := dbToLin(g0), dbToLin(g1)
	step := (lin1 - lin0) / FrameSamples
	g := lin0
	for i, v := range in {
		g += step
		f[i] = toInt16(softClip(v * g))
	}
}
