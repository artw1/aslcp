// Package diag builds the support diagnostics report (GET /api/diagnostics),
// the About data, and the redaction that makes a report safe to attach to a
// public bug report.
package diag

import (
	"strings"
	"sync"
)

// Ring is an io.Writer that keeps the last N lines written to it. Hook it
// into the server logger with io.MultiWriter.
type Ring struct {
	mu      sync.Mutex
	max     int
	lines   []string
	partial string
}

// NewRing returns a Ring holding at most max lines.
func NewRing(max int) *Ring {
	if max < 1 {
		max = 1
	}
	return &Ring{max: max}
}

// Write implements io.Writer. It never fails.
func (r *Ring) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	data := r.partial + string(p)
	parts := strings.Split(data, "\n")
	r.partial = parts[len(parts)-1]
	if len(r.partial) > 8192 { // a runaway line without newline
		r.partial = r.partial[len(r.partial)-8192:]
	}
	for _, l := range parts[:len(parts)-1] {
		r.lines = append(r.lines, strings.TrimRight(l, "\r"))
	}
	if over := len(r.lines) - r.max; over > 0 {
		r.lines = append([]string(nil), r.lines[over:]...)
	}
	return len(p), nil
}

// Lines returns the last n complete lines (all of them when n <= 0).
func (r *Ring) Lines(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.lines
	if n > 0 && len(l) > n {
		l = l[len(l)-n:]
	}
	return append([]string(nil), l...)
}
