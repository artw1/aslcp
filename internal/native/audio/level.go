package audio

import (
	"math"
	"time"
)

// Level report thresholds, for the echo test and the radio tune page.
//
//   - A frame is voiced when its RMS is at or above VoicedFloorDb. Pauses
//     and background noise below it are left out of the voiced RMS so they
//     do not drag it down.
//   - A sample is clipped at |s| >= ClipSample (32767 is full scale; -32768
//     is as well, so the test is on the magnitude of the 16-bit range).
//   - "clipping" is warned when any sample clipped or the peak reaches
//     ClipPeakDb (-0.5 dBFS: so close to full scale that the radio's
//     deviation limiter or the sound chip almost certainly clips).
//   - "too quiet" is warned when the voiced RMS is below QuietRMSDb
//     (-30 dBFS, 10 dB under the bottom of the -20 to -6 dBFS target
//     band for voice), or when there is no voiced audio at all.
const (
	VoicedFloorDb = -50.0
	ClipSample    = 32767
	ClipPeakDb    = -0.5
	QuietRMSDb    = -30.0
	// FloorDb is the level reported for silence.
	FloorDb = silenceDb
)

// Warnings in a Report.
const (
	WarnClipping = "clipping"
	WarnTooQuiet = "too quiet"
)

// Report is the level report of one recording.
type Report struct {
	At         time.Time `json:"at"`
	Frames     int       `json:"frames"`
	DurationMs int       `json:"durationMs"`
	// PeakDb is the highest sample, RMSDb the RMS over the voiced frames,
	// both in dBFS (FloorDb for silence).
	PeakDb       float64  `json:"peakDb"`
	RMSDb        float64  `json:"rmsDb"`
	VoicedFrames int      `json:"voicedFrames"`
	Clipped      int      `json:"clipped"` // samples at full scale
	Warnings     []string `json:"warnings,omitempty"`
}

// Analyzer accumulates the statistics of a recording frame by frame.
type Analyzer struct {
	frames, voiced, clipped int
	peak                    int32
	sumSq                   float64 // over voiced frames
}

// Add counts one frame.
func (a *Analyzer) Add(f *Frame) {
	var sq float64
	var pk int32
	for _, s := range f {
		v := int32(s)
		if v < 0 {
			v = -v
		}
		if v > pk {
			pk = v
		}
		if v >= ClipSample {
			a.clipped++
		}
		sq += float64(s) * float64(s)
	}
	a.frames++
	if pk > a.peak {
		a.peak = pk
	}
	if linToDb(math.Sqrt(sq/FrameSamples)/32768) >= VoicedFloorDb {
		a.voiced++
		a.sumSq += sq
	}
}

// Report returns the statistics so far, stamped at.
func (a *Analyzer) Report(at time.Time) Report {
	r := Report{
		At: at, Frames: a.frames, DurationMs: a.frames * int(FrameDuration/time.Millisecond),
		PeakDb: linToDb(float64(a.peak) / 32768), RMSDb: silenceDb,
		VoicedFrames: a.voiced, Clipped: a.clipped,
	}
	if a.voiced > 0 {
		r.RMSDb = linToDb(math.Sqrt(a.sumSq/float64(a.voiced*FrameSamples)) / 32768)
	}
	if a.clipped > 0 || r.PeakDb >= ClipPeakDb {
		r.Warnings = append(r.Warnings, WarnClipping)
	}
	if a.frames > 0 && r.RMSDb < QuietRMSDb {
		r.Warnings = append(r.Warnings, WarnTooQuiet)
	}
	return r
}

// Analyze reports on frames.
func Analyze(at time.Time, frames []Frame) Report {
	var a Analyzer
	for i := range frames {
		a.Add(&frames[i])
	}
	return a.Report(at)
}

// RollingFrames is the window of a Rolling meter: 5 frames, 100 ms.
const RollingFrames = 5

// Rolling is the peak and RMS level over the last RollingFrames frames. The
// zero value is ready; it is not safe for concurrent use.
type Rolling struct {
	sq  [RollingFrames]float64
	pk  [RollingFrames]int32
	n   int
	pos int
}

// Add records one frame.
func (r *Rolling) Add(f *Frame) {
	var sq float64
	var pk int32
	for _, s := range f {
		v := int32(s)
		if v < 0 {
			v = -v
		}
		if v > pk {
			pk = v
		}
		sq += float64(s) * float64(s)
	}
	r.sq[r.pos], r.pk[r.pos] = sq, pk
	r.pos = (r.pos + 1) % RollingFrames
	if r.n < RollingFrames {
		r.n++
	}
}

// Levels returns the peak and RMS of the window in dBFS (FloorDb when it
// is empty).
func (r *Rolling) Levels() (peakDb, rmsDb float64) {
	if r.n == 0 {
		return silenceDb, silenceDb
	}
	var sq float64
	var pk int32
	for i := 0; i < r.n; i++ {
		sq += r.sq[i]
		if r.pk[i] > pk {
			pk = r.pk[i]
		}
	}
	return linToDb(float64(pk) / 32768), linToDb(math.Sqrt(sq/float64(r.n*FrameSamples)) / 32768)
}
