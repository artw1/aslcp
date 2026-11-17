package ami

import (
	"sync"
	"sync/atomic"
)

// fan delivers values to subscribers without ever blocking the publisher.
// With keepLatest, a full subscriber buffer discards its oldest value so the
// newest one is always delivered (used for state); otherwise the new value is
// dropped and counted.
type fan[T any] struct {
	mu         sync.RWMutex
	subs       map[chan T]struct{}
	closed     bool
	keepLatest bool
	dropped    atomic.Uint64
}

func (f *fan[T]) subscribe(buf int, initial *T) (<-chan T, func()) {
	if buf < 1 {
		buf = 1
	}
	ch := make(chan T, buf)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		close(ch)
		return ch, func() {}
	}
	if f.subs == nil {
		f.subs = make(map[chan T]struct{})
	}
	f.subs[ch] = struct{}{}
	if initial != nil {
		ch <- *initial
	}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			defer f.mu.Unlock()
			if _, ok := f.subs[ch]; ok {
				delete(f.subs, ch)
				close(ch)
			}
		})
	}
}

func (f *fan[T]) publish(v T) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	for ch := range f.subs {
		select {
		case ch <- v:
			continue
		default:
		}
		if f.keepLatest {
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- v:
			default:
				f.dropped.Add(1)
			}
		} else {
			f.dropped.Add(1)
		}
	}
}

func (f *fan[T]) closeAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = true
	for ch := range f.subs {
		close(ch)
		delete(f.subs, ch)
	}
}
