package iax2

import (
	"testing"
	"time"
)

func mark(v int16) Frame {
	var f Frame
	for i := range f {
		f[i] = v
	}
	return f
}

type jbStep struct {
	put  []int64 // timestamps to put at this step (frame value = ts)
	want PlayStatus
	val  int16 // expected sample value for PlayFrame / PlayLoss
}

func TestJitterBuffer(t *testing.T) {
	cases := []struct {
		name  string
		steps []jbStep
		stats JitterStats
	}{
		{
			name: "reorders",
			steps: []jbStep{
				{put: []int64{40, 0, 20}, want: PlayIdle},
				{want: PlayIdle},
				{want: PlayFrame, val: 0},
				{want: PlayFrame, val: 20},
				{want: PlayFrame, val: 40},
				{want: PlayIdle},
			},
			stats: JitterStats{Played: 3},
		},
		{
			name: "conceals one loss",
			steps: []jbStep{
				{put: []int64{100, 120, 160, 180}, want: PlayIdle},
				{want: PlayIdle},
				{want: PlayFrame, val: 100},
				{want: PlayFrame, val: 120},
				{want: PlayLoss, val: 60}, // half of the last frame
				{want: PlayFrame, val: 160},
				{want: PlayFrame, val: 180},
				{want: PlayIdle},
			},
			stats: JitterStats{Played: 4, Lost: 1},
		},
		{
			name: "second loss is silence",
			steps: []jbStep{
				{put: []int64{0, 80}, want: PlayIdle},
				{want: PlayIdle},
				{want: PlayFrame, val: 0},
				{want: PlayLoss, val: 0},
				{want: PlayLoss, val: 0},
				{want: PlayLoss, val: 0},
				{want: PlayFrame, val: 80},
			},
			stats: JitterStats{Played: 2, Lost: 3},
		},
		{
			name: "late and duplicate frames dropped",
			steps: []jbStep{
				{put: []int64{0, 20}, want: PlayIdle},
				{want: PlayIdle},
				{want: PlayFrame, val: 0},
				{put: []int64{0, 20, 40}, want: PlayFrame, val: 20},
				{want: PlayFrame, val: 40},
			},
			stats: JitterStats{Played: 3, Late: 1},
		},
		{
			name: "underrun rebuffers",
			steps: []jbStep{
				{put: []int64{0}, want: PlayIdle},
				{want: PlayIdle},
				{want: PlayFrame, val: 0},
				{want: PlayIdle},
				{put: []int64{20}, want: PlayIdle},
				{want: PlayIdle},
				{want: PlayFrame, val: 20},
			},
			stats: JitterStats{Played: 2},
		},
		{
			name: "new timeline after a long gap",
			steps: []jbStep{
				{put: []int64{0, 20}, want: PlayIdle},
				{want: PlayIdle},
				{want: PlayFrame, val: 0},
				{put: []int64{5000}, want: PlayFrame, val: 20},
				{want: PlayFrame, val: 5000 % 32768},
			},
			stats: JitterStats{Played: 3},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			jb := NewJitterBuffer(JitterConfig{MinDelay: 40 * time.Millisecond, MaxDelay: 40 * time.Millisecond})
			now := time.Unix(1000, 0)
			for i, st := range tc.steps {
				for _, ts := range st.put {
					jb.Put(ts, mark(int16(ts)), now)
				}
				f, got := jb.Get(now)
				if got != st.want {
					t.Fatalf("step %d: status %d, want %d", i, got, st.want)
				}
				if got != PlayIdle && f[0] != st.val {
					t.Fatalf("step %d: value %d, want %d", i, f[0], st.val)
				}
				now = now.Add(FrameDuration)
			}
			s := jb.Stats()
			if s.Played != tc.stats.Played || s.Lost != tc.stats.Lost || s.Late != tc.stats.Late {
				t.Fatalf("stats %+v, want %+v", s, tc.stats)
			}
		})
	}
}

func TestJitterBufferAdapts(t *testing.T) {
	jb := NewJitterBuffer(JitterConfig{})
	if got := jb.Stats().Target; got != DefaultJitterMin {
		t.Fatalf("initial target %v", got)
	}
	// Frames 20 ms apart in timestamp arrive alternately 0 and 80 ms apart.
	now := time.Unix(0, 0)
	for i := 0; i < 64; i++ {
		if i%2 == 1 {
			now = now.Add(80 * time.Millisecond)
		}
		jb.Put(int64(i*20), Frame{}, now)
		jb.Get(now)
	}
	s := jb.Stats()
	if s.Jitter < 20*time.Millisecond || s.Target <= DefaultJitterMin || s.Target > DefaultJitterMax {
		t.Fatalf("jitter %v target %v", s.Jitter, s.Target)
	}
}

func TestJitterBufferBoundsDelay(t *testing.T) {
	jb := NewJitterBuffer(JitterConfig{MinDelay: 40 * time.Millisecond, MaxDelay: 40 * time.Millisecond, MaxFrames: 8})
	now := time.Unix(0, 0)
	for ts := int64(0); ts < 200; ts += 20 {
		jb.Put(ts, mark(int16(ts)), now)
	}
	if s := jb.Stats(); s.Dropped != 2 {
		t.Fatalf("overflow dropped %d, want 2", s.Dropped)
	}
	now = now.Add(40 * time.Millisecond)
	played := 0
	for i := 0; i < 20; i++ {
		if _, st := jb.Get(now); st == PlayFrame {
			played++
		}
	}
	if s := jb.Stats(); s.Dropped <= 2 || played+int(s.Dropped) != 10 {
		t.Fatalf("played %d stats %+v", played, s)
	}
}
