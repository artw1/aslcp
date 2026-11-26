package audio

import (
	"sync"
	"time"
)

// FrameDuration is the length of one frame.
const FrameDuration = 20 * time.Millisecond

// Clock paces the node at one tick per frame.
type Clock interface {
	// C delivers a tick every FrameDuration.
	C() <-chan time.Time
	// Now is the clock's current time (for meters).
	Now() time.Time
	// Stop releases the clock.
	Stop()
}

// RealClock is a Clock driven by a time.Ticker.
type RealClock struct{ t *time.Ticker }

// NewRealClock starts a 20 ms ticker.
func NewRealClock() *RealClock { return &RealClock{t: time.NewTicker(FrameDuration)} }

// C implements Clock.
func (c *RealClock) C() <-chan time.Time { return c.t.C }

// Now implements Clock.
func (c *RealClock) Now() time.Time { return time.Now() }

// Stop implements Clock.
func (c *RealClock) Stop() { c.t.Stop() }

// FakeClock is a manually stepped Clock for tests. Its time starts at start and
// moves one frame per Tick.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
	ch  chan time.Time
}

// NewFakeClock returns a fake clock at start (a fixed time if zero).
func NewFakeClock(start time.Time) *FakeClock {
	if start.IsZero() {
		start = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return &FakeClock{now: start, ch: make(chan time.Time)}
}

// C implements Clock.
func (c *FakeClock) C() <-chan time.Time { return c.ch }

// Now implements Clock.
func (c *FakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }

// Stop implements Clock.
func (c *FakeClock) Stop() {}

// Tick advances one frame and delivers the tick, blocking until a consumer
// receives it.
func (c *FakeClock) Tick() {
	c.mu.Lock()
	c.now = c.now.Add(FrameDuration)
	t := c.now
	c.mu.Unlock()
	c.ch <- t
}

// Advance moves the time without delivering a tick.
func (c *FakeClock) Advance(d time.Duration) { c.mu.Lock(); c.now = c.now.Add(d); c.mu.Unlock() }
