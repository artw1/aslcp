package register

import "time"

// Clock abstracts time so tests can drive refresh timing.
type Clock interface {
	Now() time.Time
	// After returns a channel that receives once d has elapsed.
	After(d time.Duration) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time                         { return time.Now() }
func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
