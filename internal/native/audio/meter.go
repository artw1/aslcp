package audio

import (
	"math"
	"sync"
	"time"
)

// Level meters in dBFS, modeled on internal/dv/bridge/meter.go: a live level
// that falls away like a meter when audio stops.

const (
	meterDecayDbSec = 20.0
	meterActiveFor  = 250 * time.Millisecond
	clipLevel       = 32000 // a sample at or above this counts as clipped
)

// Level is the measurement of one frame.
type Level struct {
	RMSDb, PeakDb float64
	Clipped       bool // a sample at or near full scale
}

// Measure returns the RMS and peak level of f in dBFS (-90 for silence).
func Measure(f *Frame) Level {
	var sum float64
	var peak int32
	clipped := false
	for _, s := range f {
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
	return Level{
		RMSDb:   linToDb(math.Sqrt(sum/FrameSamples) / 32768),
		PeakDb:  linToDb(float64(peak) / 32768),
		Clipped: clipped,
	}
}

// Meter is a live level meter. It is safe for concurrent use.
type Meter struct {
	mu      sync.Mutex
	lastAt  time.Time
	rms, pk float64
	clipped int
	frames  int
}

// Observe records one frame heard at now.
func (m *Meter) Observe(now time.Time, f *Frame) {
	l := Measure(f)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.frames == 0 {
		m.rms, m.pk = silenceDb, silenceDb
	} else {
		dt := now.Sub(m.lastAt)
		m.rms, m.pk = decay(m.rms, dt), decay(m.pk, dt)
	}
	m.rms, m.pk = math.Max(m.rms, l.RMSDb), math.Max(m.pk, l.PeakDb)
	if l.Clipped {
		m.clipped++
	}
	m.frames++
	m.lastAt = now
}

func decay(v float64, dt time.Duration) float64 {
	return math.Max(v-meterDecayDbSec*dt.Seconds(), silenceDb)
}

// Reading is a snapshot of a Meter.
type Reading struct {
	RMSDb, PeakDb float64
	Active        bool // a frame arrived recently
	Clipped       int  // frames with clipped samples since Reset
	Frames        int
}

// Snapshot returns the levels as of now, decayed since the last frame.
func (m *Meter) Snapshot(now time.Time) Reading {
	m.mu.Lock()
	defer m.mu.Unlock()
	r := Reading{RMSDb: silenceDb, PeakDb: silenceDb, Clipped: m.clipped, Frames: m.frames}
	if m.lastAt.IsZero() {
		return r
	}
	dt := now.Sub(m.lastAt)
	r.RMSDb, r.PeakDb = decay(m.rms, dt), decay(m.pk, dt)
	r.Active = dt <= meterActiveFor
	return r
}

// Reset clears the meter.
func (m *Meter) Reset() {
	m.mu.Lock()
	*m = Meter{}
	m.mu.Unlock()
}
