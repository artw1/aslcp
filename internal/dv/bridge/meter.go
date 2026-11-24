package bridge

import (
	"math"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

// The level meter of one audio direction. Each frame is measured twice, after
// the static gain: before the AGC and after it. The live levels fall away like
// a meter when audio stops; a summary of the finished transmission is kept for
// the panel ("Last over: ...").

const (
	meterDecayDbSec = 20.0                   // how fast a live level falls
	meterActiveFor  = 250 * time.Millisecond // audio flowing: a frame in this time
)

// level is one frame's measurement.
type level struct {
	rmsDb, peakDb float64
	clipped       bool // a sample at or near full scale
}

func measure(p *[vocoder.PCMSamples]int16) level {
	var sum float64
	var peak int32
	clipped := false
	for _, s := range p {
		v := int32(s)
		if v < 0 {
			v = -v
		}
		if v > peak {
			peak = v
		}
		if v >= clipLevel {
			clipped = true
		}
		sum += float64(s) * float64(s)
	}
	return level{
		rmsDb:   linToDb(math.Sqrt(sum/vocoder.PCMSamples) / 32768),
		peakDb:  linToDb(float64(peak) / 32768),
		clipped: clipped,
	}
}

// Verdict judges the level of the speech before the AGC against the target:
// low when it is more than 10 dB under, high when it is more than 8 dB over or
// any frame clipped, else ok.
func Verdict(speechDb float64, clipped int, target int) string {
	t := float64(target)
	switch {
	case clipped > 0 || speechDb > t+8:
		return ctl.VerdictHigh
	case speechDb < t-10:
		return ctl.VerdictLow
	}
	return ctl.VerdictOK
}

// dirMeter is the meter of one direction. A mutex guards it so that a reader
// on another goroutine sees a consistent state.
type dirMeter struct {
	mu sync.Mutex
	g  gate

	// live
	lastAt                     time.Time
	peak, rms, outPeak, outRms float64
	agc                        bool
	// the transmission in progress
	frames, speech      int
	sumPow, sumOutPow   float64
	maxPeak, maxOutPeak float64
	clipped             int
	last                *ctl.AudioSummary
}

func newDirMeter() *dirMeter { m := &dirMeter{}; m.begin(); return m }

// begin starts a new transmission; the live levels drop to silence, the last
// summary stays.
func (m *dirMeter) begin() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.g.reset()
	m.lastAt = time.Time{}
	m.peak, m.rms, m.outPeak, m.outRms = silenceDb, silenceDb, silenceDb, silenceDb
	m.frames, m.speech, m.clipped = 0, 0, 0
	m.sumPow, m.sumOutPow = 0, 0
	m.maxPeak, m.maxOutPeak = silenceDb, silenceDb
}

func decay(v float64, dt time.Duration) float64 {
	return math.Max(v-meterDecayDbSec*dt.Seconds(), silenceDb)
}

func pow(db float64) float64 { return math.Pow(10, db/10) }

// frame records one frame: pre and post are its levels before and after the AGC.
func (m *dirMeter) frame(now time.Time, pre, post level, agc bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.lastAt.IsZero() {
		dt := now.Sub(m.lastAt)
		m.peak, m.rms = decay(m.peak, dt), decay(m.rms, dt)
		m.outPeak, m.outRms = decay(m.outPeak, dt), decay(m.outRms, dt)
	}
	m.peak, m.rms = math.Max(m.peak, pre.peakDb), math.Max(m.rms, pre.rmsDb)
	m.outPeak, m.outRms = math.Max(m.outPeak, post.peakDb), math.Max(m.outRms, post.rmsDb)
	m.lastAt, m.agc = now, agc
	m.frames++
	if m.g.update(pre.rmsDb) == gateOpen {
		m.speech++
		m.sumPow += pow(pre.rmsDb)
		m.sumOutPow += pow(post.rmsDb)
	}
	m.maxPeak, m.maxOutPeak = math.Max(m.maxPeak, pre.peakDb), math.Max(m.maxOutPeak, post.peakDb)
	if pre.clipped {
		m.clipped++
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func meanDb(sum float64, n int) float64 {
	if n == 0 {
		return silenceDb
	}
	return math.Max(10*math.Log10(sum/float64(n)), silenceDb)
}

// end closes the transmission and keeps its summary. A call without audio
// leaves the previous summary.
func (m *dirMeter) end(now time.Time, dur time.Duration, target int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frames == 0 {
		return
	}
	sp := meanDb(m.sumPow, m.speech)
	m.last = &ctl.AudioSummary{
		SpeechDb: round1(sp), OutSpeechDb: round1(meanDb(m.sumOutPow, m.speech)),
		PeakDb: round1(m.maxPeak), OutPeakDb: round1(m.maxOutPeak),
		Clipped: m.clipped, DurationMs: dur.Milliseconds(),
		Verdict: Verdict(sp, m.clipped, target), AGC: m.agc, At: now,
	}
}

// snapshot is the meter for the status: levels fallen by the time since the
// last frame.
func (m *dirMeter) snapshot(now time.Time, agcOn bool) ctl.AudioDir {
	m.mu.Lock()
	defer m.mu.Unlock()
	d := ctl.AudioDir{AGC: agcOn, Last: m.last}
	age := now.Sub(m.lastAt)
	d.Active = !m.lastAt.IsZero() && age < meterActiveFor
	if m.lastAt.IsZero() {
		d.PeakDb, d.RmsDb, d.OutPeakDb, d.OutRmsDb = silenceDb, silenceDb, silenceDb, silenceDb
		return d
	}
	d.PeakDb, d.RmsDb = round1(decay(m.peak, age)), round1(decay(m.rms, age))
	d.OutPeakDb, d.OutRmsDb = round1(decay(m.outPeak, age)), round1(decay(m.outRms, age))
	return d
}

// audioPath is one direction's AGC and meter.
type audioPath struct {
	agc   *AGC
	m     *dirMeter
	agcOn bool
}

func newAudioPath() *audioPath { return &audioPath{agc: NewAGC(), m: newDirMeter()} }

// begin starts a call.
func (p *audioPath) begin() { p.agc.Reset(); p.m.begin(); p.agcOn = false }

// process measures a frame (already through the static gain), applies the AGC
// when it is on, and measures the result. With the AGC off the frame is not
// touched.
func (p *audioPath) process(now time.Time, pcm *[vocoder.PCMSamples]int16, on bool, prm AGCParams) {
	pre := measure(pcm)
	post := pre
	if on {
		if !p.agcOn {
			p.agc.Reset() // switched on in mid-call: start from 0 dB
		}
		p.agc.Process(pcm, prm)
		post = measure(pcm)
	}
	p.agcOn = on
	p.m.frame(now, pre, post, on)
}

// end closes the call.
func (p *audioPath) end(now time.Time, dur time.Duration, target int) { p.m.end(now, dur, target) }
