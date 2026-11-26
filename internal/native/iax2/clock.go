package iax2

import "time"

// Clock abstracts time so tests can drive the call's timers.
type Clock interface {
	Now() time.Time
	// NewTicker returns a ticker that fires every d.
	NewTicker(d time.Duration) Ticker
}

// Ticker is the part of time.Ticker the call uses.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTicker(d time.Duration) Ticker { return realTicker{time.NewTicker(d)} }

type realTicker struct{ t *time.Ticker }

func (r realTicker) C() <-chan time.Time { return r.t.C }
func (r realTicker) Stop()               { r.t.Stop() }
