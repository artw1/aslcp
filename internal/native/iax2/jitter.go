package iax2

import "time"

// PlayStatus says what JitterBuffer.Get produced.
type PlayStatus int

const (
	// PlayIdle means nothing to play: no talk spurt, or still buffering.
	PlayIdle PlayStatus = iota
	// PlayFrame means a received frame.
	PlayFrame
	// PlayLoss means a frame was missing and a concealment frame was made.
	PlayLoss
)

// Jitter buffer defaults.
const (
	DefaultJitterMin    = 40 * time.Millisecond
	DefaultJitterMax    = 300 * time.Millisecond
	DefaultJitterFrames = 50
	// jitterResync is the gap after which a frame far ahead of the playout
	// point starts a new timeline instead of counting as loss.
	jitterResync = 1000
	// jitterLateWindow bounds how far behind the last played frame a
	// timestamp may be and still be treated as late (rather than as a new
	// timeline).
	jitterLateWindow = 10000
)

// JitterConfig tunes a JitterBuffer. Zero fields take the defaults.
type JitterConfig struct {
	MinDelay  time.Duration
	MaxDelay  time.Duration
	MaxFrames int
}

// JitterStats are jitter buffer counters.
type JitterStats struct {
	Played  uint64        // frames played
	Lost    uint64        // gaps concealed
	Late    uint64        // frames that arrived after their playout time
	Dropped uint64        // frames dropped to bound delay or on overflow
	Jitter  time.Duration // smoothed interarrival jitter (RFC 3550 style)
	Target  time.Duration // current playout delay target
}

// JitterBuffer is a small adaptive playout buffer for 20 ms frames keyed by
// timestamp in milliseconds. Each talk spurt starts after the target delay,
// which follows the measured jitter between MinDelay and MaxDelay. Frames
// are reordered by timestamp, a missing frame is concealed by repeating the
// last frame at half level once and silence after that, and an empty buffer
// ends the spurt so the next frame buffers again. It is not safe for
// concurrent use; the call loop owns it.
type JitterBuffer struct {
	cfg JitterConfig

	buf     map[int64]Frame
	playing bool
	startAt time.Time
	next    int64

	lastPlayed   int64
	havePlayed   bool
	last         Frame
	lossRun      int
	haveArrival  bool
	lastArrival  time.Time
	lastArriveTS int64
	jitterMS     float64

	stats JitterStats
}

// NewJitterBuffer returns an empty buffer.
func NewJitterBuffer(cfg JitterConfig) *JitterBuffer {
	if cfg.MinDelay <= 0 {
		cfg.MinDelay = DefaultJitterMin
	}
	if cfg.MaxDelay < cfg.MinDelay {
		cfg.MaxDelay = DefaultJitterMax
		if cfg.MaxDelay < cfg.MinDelay {
			cfg.MaxDelay = cfg.MinDelay
		}
	}
	if cfg.MaxFrames <= 0 {
		cfg.MaxFrames = DefaultJitterFrames
	}
	return &JitterBuffer{cfg: cfg, buf: make(map[int64]Frame)}
}

// target is the playout delay for the next spurt.
func (j *JitterBuffer) target() time.Duration {
	d := time.Duration((frameMS + 3*j.jitterMS) * float64(time.Millisecond))
	if d < j.cfg.MinDelay {
		d = j.cfg.MinDelay
	}
	if d > j.cfg.MaxDelay {
		d = j.cfg.MaxDelay
	}
	return d
}

// Put stores a frame with timestamp ts (ms) that arrived at now.
func (j *JitterBuffer) Put(ts int64, f Frame, now time.Time) {
	j.measure(ts, now)
	if j.havePlayed && ts <= j.lastPlayed && j.lastPlayed-ts < jitterLateWindow {
		j.stats.Late++
		return
	}
	if j.playing && ts < j.next && j.next-ts < jitterLateWindow {
		j.stats.Late++
		return
	}
	if _, dup := j.buf[ts]; dup {
		return
	}
	if len(j.buf) >= j.cfg.MaxFrames {
		delete(j.buf, j.minTS())
		j.stats.Dropped++
	}
	j.buf[ts] = f
	if !j.playing && len(j.buf) == 1 {
		j.startAt = now.Add(j.target())
	}
}

func (j *JitterBuffer) measure(ts int64, now time.Time) {
	if j.haveArrival {
		d := float64(now.Sub(j.lastArrival))/float64(time.Millisecond) - float64(ts-j.lastArriveTS)
		if d < 0 {
			d = -d
		}
		if d < jitterResync {
			j.jitterMS += (d - j.jitterMS) / 16
		}
	}
	j.haveArrival, j.lastArrival, j.lastArriveTS = true, now, ts
}

func (j *JitterBuffer) minTS() int64 {
	first := true
	var m int64
	for ts := range j.buf {
		if first || ts < m {
			m, first = ts, false
		}
	}
	return m
}

func (j *JitterBuffer) maxTS() int64 {
	first := true
	var m int64
	for ts := range j.buf {
		if first || ts > m {
			m, first = ts, false
		}
	}
	return m
}

// Get returns the frame due at now; call it once per 20 ms.
func (j *JitterBuffer) Get(now time.Time) (Frame, PlayStatus) {
	if !j.playing {
		if len(j.buf) == 0 || now.Before(j.startAt) {
			return Frame{}, PlayIdle
		}
		j.playing = true
		j.next = j.minTS()
	}
	if len(j.buf) == 0 {
		// Underrun: end the spurt; the next frame buffers again.
		j.playing = false
		return Frame{}, PlayIdle
	}
	if first := j.minTS(); first > j.next+jitterResync || first < j.next-jitterLateWindow {
		j.next = first
	}
	// Bound the delay: if more than three frames beyond the target are
	// queued, skip the oldest. A frame far ahead (a new timeline) is not
	// queue depth.
	maxDepth := j.target().Milliseconds() + 3*frameMS
	if depth := j.maxTS() - j.next + frameMS; depth > maxDepth && depth <= jitterResync {
		if _, ok := j.buf[j.next]; ok {
			delete(j.buf, j.next)
			j.stats.Dropped++
		}
		j.next += frameMS
	}
	if f, ok := j.buf[j.next]; ok {
		delete(j.buf, j.next)
		j.lastPlayed, j.havePlayed = j.next, true
		j.next += frameMS
		j.last, j.lossRun = f, 0
		j.stats.Played++
		return f, PlayFrame
	}
	j.next += frameMS
	j.lossRun++
	j.stats.Lost++
	var c Frame
	if j.lossRun == 1 {
		for i, s := range j.last {
			c[i] = s / 2
		}
	}
	return c, PlayLoss
}

// Reset empties the buffer and keeps the jitter estimate.
func (j *JitterBuffer) Reset() {
	clear(j.buf)
	j.playing = false
	j.lossRun = 0
}

// Stats returns the counters.
func (j *JitterBuffer) Stats() JitterStats {
	s := j.stats
	s.Jitter = time.Duration(j.jitterMS * float64(time.Millisecond))
	s.Target = j.target()
	return s
}
