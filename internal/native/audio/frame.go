// Package audio is the shared audio core of the native node: the frame type,
// AGC, mixer, tone, CW and WAV sources, the 20 ms frame clock and level meters.
//
// All audio is signed 16-bit linear PCM, mono, 8000 Hz, in 20 ms frames of 160
// samples.
package audio

import "math"

// Audio format constants shared by every native package.
const (
	// SampleRate is the sample rate in Hz.
	SampleRate = 8000
	// FrameSamples is the number of samples in one 20 ms frame.
	FrameSamples = 160
)

// Frame is one 20 ms frame of mono 16-bit PCM at 8 kHz.
type Frame [FrameSamples]int16

// silenceDb is the level reported for silence.
const silenceDb = -90.0

func dbToLin(db float64) float64 { return math.Pow(10, db/20) }

func linToDb(v float64) float64 {
	if v <= 0 {
		return silenceDb
	}
	return math.Max(20*math.Log10(v), silenceDb)
}

// softClip is linear up to the knee and bends smoothly towards, but never
// reaches, 1.
func softClip(x float64) float64 {
	const k = agcKnee
	ax := math.Abs(x)
	if ax <= k {
		return x
	}
	y := k + (1-k)*math.Tanh((ax-k)/(1-k))
	if x < 0 {
		return -y
	}
	return y
}

func toInt16(v float64) int16 {
	o := math.Round(v * 32768)
	if o > math.MaxInt16 {
		return math.MaxInt16
	}
	if o < math.MinInt16 {
		return math.MinInt16
	}
	return int16(o)
}
