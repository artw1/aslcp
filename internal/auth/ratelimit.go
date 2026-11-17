package auth

import (
	"strings"
	"sync"
	"time"
)

// Limiter limits failed logins per key (IP + username): Max failures within
// Window block further attempts until the oldest failure ages out.
type Limiter struct {
	Max    int
	Window time.Duration
	Now    func() time.Time

	mu       sync.Mutex
	failures map[string][]time.Time
}

// NewLimiter returns the default limiter: 5 failures per 5 minutes.
func NewLimiter() *Limiter {
	return &Limiter{Max: 5, Window: 5 * time.Minute, Now: time.Now, failures: map[string][]time.Time{}}
}

// Key builds the limiter key.
func Key(ip, username string) string { return ip + "|" + strings.ToLower(username) }

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	fs := l.failures[key]
	i := 0
	for i < len(fs) && now.Sub(fs[i]) >= l.Window {
		i++
	}
	fs = fs[i:]
	if len(fs) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = fs
	return fs
}

// Allow reports whether an attempt may proceed; if not, retryAfter says when.
func (l *Limiter) Allow(key string) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	fs := l.prune(key, now)
	if len(fs) >= l.Max {
		return false, fs[0].Add(l.Window).Sub(now)
	}
	return true, 0
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	l.prune(key, now)
	l.failures[key] = append(l.failures[key], now)
	if len(l.failures) > 10000 { // bound memory under a spray attack
		for k := range l.failures {
			if l.prune(k, now) == nil {
				continue
			}
		}
	}
}

// Reset clears failures for key (after a successful login).
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}
