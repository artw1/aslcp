// Package activity records transmissions (link keyups and local receiver
// keyups) seen by the monitor, for the "recent activity" lists.
package activity

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

// Local is the Link value of a keyup of the local receiver.
const Local = "local"

const (
	// MinDuration: shorter keyups are glitches (a flapping COS), not recorded.
	MinDuration = 500 * time.Millisecond
	// KerchunkMax: keyups shorter than this are flagged as kerchunks.
	KerchunkMax = 1500 * time.Millisecond
	// Retention is how long rows are kept; MaxRows caps the table.
	Retention = 7 * 24 * time.Hour
	MaxRows   = 10000

	pruneEvery   = 24 * time.Hour
	pruneEveryN  = 100 // also enforce the cap after this many inserts
	maxOpenTimes = 4096
)

// Store is what the recorder needs from the database.
type Store interface {
	AddActivity(a store.Activity) error
	PruneActivity(cutoff time.Time, maxRows int) (int64, error)
}

// Recorder turns monitor updates into activity rows. It tracks when each
// link (and each local receiver) keyed and records the transmission when it
// unkeys. Safe for concurrent use.
type Recorder struct {
	Store  Store
	Now    func() time.Time
	Logger *log.Logger
	// OnRecord is called (without locks held) after a row was stored.
	OnRecord func(store.Activity)

	mu     sync.Mutex
	open   map[key]time.Time
	nInsrt int
}

type key struct{ node, link string }

// New returns a Recorder.
func New(st Store, logger *log.Logger) *Recorder {
	if logger == nil {
		logger = log.Default()
	}
	return &Recorder{Store: st, Now: time.Now, Logger: logger, open: map[key]time.Time{}}
}

// Process updates the recorder's view of one node from a monitor update.
// Every update carries the full status, so the function is idempotent.
func (r *Recorder) Process(u rpt.Update) {
	st := u.Status
	now := r.Now()
	var done []store.Activity

	r.mu.Lock()
	if st.Stale {
		// The state is unknown: forget open keyups rather than guess.
		for k := range r.open {
			if k.node == st.Node {
				delete(r.open, k)
			}
		}
		r.mu.Unlock()
		return
	}
	keyed := map[string]bool{}
	if st.RxKeyed {
		keyed[Local] = true
	}
	for _, l := range st.Links {
		// Local monitor links are not audio from the network.
		if l.Keyed && l.Mode != rpt.ModeLocalMonitor {
			keyed[l.Node] = true
		}
	}
	for link := range keyed {
		k := key{st.Node, link}
		if _, ok := r.open[k]; !ok && len(r.open) < maxOpenTimes {
			r.open[k] = now
		}
	}
	for k, start := range r.open {
		if k.node != st.Node || keyed[k.link] {
			continue
		}
		delete(r.open, k)
		d := now.Sub(start)
		if d < MinDuration {
			continue
		}
		done = append(done, store.Activity{Node: k.node, Link: k.link, Start: start, Duration: d, Kerchunk: d < KerchunkMax})
	}
	r.mu.Unlock()

	for _, a := range done {
		if err := r.Store.AddActivity(a); err != nil {
			r.Logger.Printf("activity: %v", err)
			continue
		}
		r.mu.Lock()
		r.nInsrt++
		prune := r.nInsrt%pruneEveryN == 0
		r.mu.Unlock()
		if prune {
			r.Prune()
		}
		if r.OnRecord != nil {
			r.OnRecord(a)
		}
	}
}

// Prune deletes rows older than the retention and enforces the row cap.
func (r *Recorder) Prune() {
	if _, err := r.Store.PruneActivity(r.Now().Add(-Retention), MaxRows); err != nil {
		r.Logger.Printf("activity prune: %v", err)
	}
}

// Run prunes at start and then daily until ctx is done.
func (r *Recorder) Run(ctx context.Context) {
	r.Prune()
	t := time.NewTicker(pruneEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.Prune()
		}
	}
}
