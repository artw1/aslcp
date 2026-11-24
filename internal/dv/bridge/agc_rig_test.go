package bridge_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/usrp"
)

// quietCall plays n constant-level frames of value v (the fake vocoder keeps
// the first sample of a frame, so each frame is one level) and returns the
// echoed voice frames.
func quietCall(t *testing.T, g *rig, v int16, n int) []int16 {
	t.Helper()
	for k := 0; k < n; k++ {
		p := usrp.Packet{Seq: uint32(k), Keyup: true, Type: usrp.TypeVoice, PCM: new([160]int16)}
		for i := range p.PCM {
			p.PCM[i] = v
		}
		g.peer.WriteToUDP(p.Append(nil), g.engine)
		time.Sleep(20 * time.Millisecond)
	}
	g.peer.WriteToUDP(voice(n, false), g.engine)
	var out []int16
	for _, p := range collect(t, g.peer, n, 5*time.Second) {
		if p.p.Type == usrp.TypeVoice && p.p.PCM != nil {
			out = append(out, p.p.PCM[0])
		}
	}
	if len(out) != n {
		t.Fatalf("%d frames echoed, want %d", len(out), n)
	}
	return out
}

func rmsOf(v []int16) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s / float64(len(v)))
}

func audioStatus(t *testing.T, g *rig) *ctl.AudioStatus {
	t.Helper()
	st, err := g.r.Status(context.Background())
	if err != nil || st.Audio == nil {
		t.Fatalf("status %+v, %v", st, err)
	}
	return st.Audio
}

func TestAGCRaisesQuietNodeAudio(t *testing.T) {
	const v, n = 300, 60                                                                              // about -40.8 dBFS
	off := newRig(t, rigOpts{cfg: func(c *ctl.Config) { c.USRP.AGCTx, c.USRP.AGCRx = false, false }}) // both AGCs explicitly off: bit-exact
	base := quietCall(t, off, v, n)
	for k, s := range base {
		if s != v {
			t.Fatalf("AGC off: frame %d = %d, want %d unchanged", k, s, v)
		}
	}
	on := newRig(t, rigOpts{cfg: func(c *ctl.Config) {
		c.USRP.AGCTx, c.USRP.AGCTarget, c.USRP.AGCMax = true, -22, 18
	}})
	got := quietCall(t, on, v, n)
	if r, b := rmsOf(got[n/2:]), rmsOf(base[n/2:]); r < 2*b {
		t.Fatalf("AGC on: output rms %.0f, want well above %.0f", r, b)
	}
	if got[0] > 2*v {
		t.Fatalf("the first frame jumped to %d: the AGC starts at 0 dB", got[0])
	}
	// the meter saw the difference
	eventually(t, 2*time.Second, func() bool { return audioStatus(t, on).Tx.Last != nil })
	a := audioStatus(t, on)
	s := a.Tx.Last
	if !a.Tx.AGC || !s.AGC || s.OutSpeechDb <= s.SpeechDb+3 || math.Abs(s.SpeechDb-(-40.8)) > 0.5 || s.Verdict != ctl.VerdictLow || a.TargetDb != -22 {
		t.Fatalf("audio %+v last %+v", a, s)
	}
	// the other direction ran with the AGC off: before equals after
	eventually(t, 2*time.Second, func() bool { return audioStatus(t, on).Rx.Last != nil })
	if r := audioStatus(t, on).Rx.Last; r.AGC || r.SpeechDb != r.OutSpeechDb {
		t.Fatalf("rx summary %+v", r)
	}
}

func TestAGCOffMetersWithoutTouchingAudio(t *testing.T) {
	g := newRig(t, rigOpts{cfg: func(c *ctl.Config) { c.USRP.AGCTx, c.USRP.AGCRx, c.USRP.AGCTarget = false, false, -22 }})
	got := quietCall(t, g, 1000, 30)
	for k, s := range got {
		if s != 1000 {
			t.Fatalf("frame %d = %d, want 1000", k, s)
		}
	}
	eventually(t, 2*time.Second, func() bool { return audioStatus(t, g).Tx.Last != nil && audioStatus(t, g).Rx.Last != nil })
	a := audioStatus(t, g)
	for name, d := range map[string]ctl.AudioDir{"tx": a.Tx, "rx": a.Rx} {
		if d.AGC || d.Last.AGC || d.Last.SpeechDb != d.Last.OutSpeechDb || math.Abs(d.Last.SpeechDb-(-30.3)) > 0.5 || d.Last.Verdict != ctl.VerdictOK {
			t.Fatalf("%s %+v last %+v", name, d, d.Last)
		}
	}
}
