package favstats

import (
	"context"
	"hash/fnv"
	"time"
)

// DemoFetcher returns synthetic samples (demo mode: no network): counters
// grow with the clock and every node keys up now and then.
func DemoFetcher(now func() time.Time) Fetcher {
	start := now()
	return func(_ context.Context, node string) (Sample, error) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(node))
		seed := int64(h.Sum32())
		if seed%9 == 8 {
			return Sample{}, errNoData // some nodes are "offline"
		}
		t := now()
		up := float64(86400*(3+seed%60)) + t.Sub(start).Seconds()
		share := 0.02 + float64(seed%25)/100 // 2%..26% lifetime TX share
		// Each node is "on air" for 20 s out of every 20+seed%90 s.
		period := int64(20 + seed%90)
		phase := (t.Unix() + seed) % period
		keyed := phase < 20
		tx := up*share + float64(int64(t.Sub(start).Seconds())/period*20)
		if keyed {
			tx += float64(phase)
		} else {
			tx += 20
		}
		return Sample{
			Uptime: up, TxTime: tx, KeyUps: tx / 6, HaveCounters: true, Keyed: keyed,
			Links:  int(seed % 7),
			Active: seed%5 != 0, WebTx: seed%3 == 0,
		}, nil
	}
}

// WithLatency makes every fetch take d (or until the context ends), so that
// the "checking now" indicator can be seen in demo mode.
func WithLatency(f Fetcher, d time.Duration) Fetcher {
	return func(ctx context.Context, node string) (Sample, error) {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return Sample{}, ctx.Err()
		}
		return f(ctx, node)
	}
}
