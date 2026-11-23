package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/dv/vocoder"
	"github.com/artw1/aslcp/internal/dv/vocoder/fake"
)

// Pass criterion (roundtrip, concurrency 1): at least 2x real time and a
// p99 latency at most 15 ms. With -fake (CI, no real md380-emu) the p99
// bound is relaxed to 50 ms because shared runners stall for longer.
const (
	passFPS     = 100.0
	passP99     = 15 * time.Millisecond
	passP99Fake = 50 * time.Millisecond
)

func sineFrame(n int) *[vocoder.PCMSamples]int16 {
	var f [vocoder.PCMSamples]int16
	for i := range f {
		t := float64(n*vocoder.PCMSamples + i)
		f[i] = int16(8000 * math.Sin(2*math.Pi*1000*t/8000))
	}
	return &f
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(math.Ceil(p*float64(len(sorted)))) - 1
	if i < 0 {
		i = 0
	}
	return sorted[i]
}

func bench(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("bench", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("md380", "127.0.0.1:2470", "md380-emu address")
	frames := fs.Int("frames", 1500, "total frames")
	mode := fs.String("mode", "roundtrip", "encode, decode or roundtrip")
	conc := fs.Int("concurrency", 1, "parallel clients")
	useFake := fs.Bool("fake", false, "use the in-process fake server (relaxes the p99 bound for codec2)")
	codec := fs.String("codec", "ambe", "ambe (md380-emu) or codec2 (native Go, in process)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *frames < 1 || *conc < 1 || (*mode != "encode" && *mode != "decode" && *mode != "roundtrip") ||
		(*codec != "ambe" && *codec != "codec2") {
		fmt.Fprintln(stderr, "aslcp-dv bench: bad flags")
		return 2
	}
	c2 := *codec == "codec2"
	if *useFake && !c2 {
		srv, err := fake.New("127.0.0.1:0")
		if err != nil {
			fmt.Fprintln(stderr, "aslcp-dv bench:", err)
			return 1
		}
		defer srv.Close()
		*addr = srv.Addr()
	}

	var mu sync.Mutex
	var lat []time.Duration
	var timeouts, done uint64
	var wg sync.WaitGroup
	per := (*frames + *conc - 1) / *conc
	start := time.Now()
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var m *vocoder.MD380
			var enc, dec *vocoder.C2 // codec2: one encoder and decoder per worker
			if c2 {
				enc, dec = vocoder.NewCodec2(), vocoder.NewCodec2()
			} else {
				m = vocoder.NewMD380(*addr, 0) // one client (two sockets) per worker
				defer m.Close()
			}
			local := make([]time.Duration, 0, per)
			ambe := vocoder.SilenceAMBE
			var c2sil [vocoder.C2Bytes]byte
			if c2 {
				c2sil = enc.Encode(new([vocoder.PCMSamples]int16))
			}
			for n := 0; n < per && ctx.Err() == nil; n++ {
				t0 := time.Now()
				switch {
				case c2:
					switch *mode {
					case "encode":
						enc.Encode(sineFrame(n))
					case "decode":
						dec.Decode(&c2sil)
					default:
						f := enc.Encode(sineFrame(n))
						dec.Decode(&f)
					}
				default:
					switch *mode {
					case "encode":
						m.Encode(ctx, sineFrame(n))
					case "decode":
						m.Decode(ctx, ambe)
					default:
						a, _ := m.Encode(ctx, sineFrame(n))
						m.Decode(ctx, a)
					}
				}
				local = append(local, time.Since(t0))
			}
			var tmo uint64
			if m != nil {
				tmo = m.Stats().Timeouts
			}
			mu.Lock()
			lat = append(lat, local...)
			timeouts += tmo
			done += uint64(len(local))
			mu.Unlock()
		}()
	}
	wg.Wait()
	el := time.Since(start)
	if done == 0 {
		fmt.Fprintln(stderr, "aslcp-dv bench: no frames completed")
		return 1
	}
	sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
	fps := float64(done) / el.Seconds()
	p99 := percentile(lat, 0.99)
	if c2 {
		fmt.Fprintf(stdout, "codec2 native mode=%s concurrency=%d frames=%d\n", *mode, *conc, done)
	} else {
		fmt.Fprintf(stdout, "md380 %s mode=%s concurrency=%d frames=%d\n", *addr, *mode, *conc, done)
	}
	fmt.Fprintf(stdout, "frames/s   %.0f\n", fps)
	fmt.Fprintf(stdout, "real-time  %.1fx\n", fps/50)
	fmt.Fprintf(stdout, "latency    p50 %v  p90 %v  p99 %v  max %v\n",
		percentile(lat, 0.50).Round(time.Microsecond), percentile(lat, 0.90).Round(time.Microsecond),
		p99.Round(time.Microsecond), lat[len(lat)-1].Round(time.Microsecond))
	fmt.Fprintf(stdout, "timeouts   %d\n", timeouts)

	if *mode != "roundtrip" || *conc != 1 {
		fmt.Fprintln(stdout, "result     no pass criterion for this mode/concurrency")
		return 0
	}
	limit := passP99
	if *useFake {
		limit = passP99Fake
	}
	if fps < passFPS || p99 > limit {
		fmt.Fprintf(stdout, "result     FAIL (need >= %.0f frames/s and p99 <= %v)\n", passFPS, limit)
		return 1
	}
	fmt.Fprintln(stdout, "result     PASS")
	return 0
}
