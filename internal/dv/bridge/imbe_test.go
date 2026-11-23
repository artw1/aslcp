package bridge_test

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/imbe"
	"github.com/artw1/aslcp/internal/dv/network"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

// Test networks: modes that exist only in this test binary.
const (
	modeTestIMBE  = "TIMBE" // carries IMBE
	modeTestMixed = "TMIX"  // carries AMBE, receives both (like YSF)
)

type testNet struct {
	mode  string
	codec network.Codec

	mu   sync.Mutex
	sink network.Sink
	tx   []network.VoiceFrame
}

var (
	testNetsMu sync.Mutex
	testNets   = map[string]*testNet{}
)

func init() {
	for mode, c := range map[string]network.Codec{modeTestIMBE: network.CodecIMBE, modeTestMixed: network.CodecAMBE} {
		mode, c := mode, c
		network.Register(mode, func(ctl.NetConfig, ctl.Identity) (network.Network, error) {
			n := &testNet{mode: mode, codec: c}
			testNetsMu.Lock()
			testNets[mode] = n
			testNetsMu.Unlock()
			return n, nil
		})
	}
}

func (n *testNet) Mode() string         { return n.mode }
func (n *testNet) Codec() network.Codec { return n.codec }
func (n *testNet) Run(ctx context.Context, s network.Sink) error {
	n.mu.Lock()
	n.sink = s
	n.mu.Unlock()
	<-ctx.Done()
	return nil
}
func (n *testNet) Tune(context.Context, ctl.Target) error { return nil }
func (n *testNet) Unlink(context.Context) error           { return nil }
func (n *testNet) StartTX(network.CallHeader) error       { return nil }
func (n *testNet) EndTX(uint64) error                     { return nil }
func (n *testNet) Status() ctl.NetStatus                  { return ctl.NetStatus{Mode: n.mode} }
func (n *testNet) WriteVoice(f network.VoiceFrame) error {
	f.Data = append([]byte(nil), f.Data...)
	n.mu.Lock()
	n.tx = append(n.tx, f)
	n.mu.Unlock()
	return nil
}

func (n *testNet) sinkNow() network.Sink {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sink
}

func (n *testNet) sent() []network.VoiceFrame {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]network.VoiceFrame(nil), n.tx...)
}

func testNetOf(t *testing.T, mode string) *testNet {
	t.Helper()
	var n *testNet
	eventually(t, 2*time.Second, func() bool {
		testNetsMu.Lock()
		n = testNets[mode]
		testNetsMu.Unlock()
		return n != nil && n.sinkNow() != nil
	})
	return n
}

func testRig(t *testing.T) *rig {
	testNetsMu.Lock()
	testNets = map[string]*testNet{} // forget the previous test's networks
	testNetsMu.Unlock()
	return newRig(t, rigOpts{mode: modeTestIMBE, cfg: func(c *ctl.Config) {
		c.Networks = append(c.Networks,
			ctl.NetConfig{Mode: modeTestIMBE, Enabled: true},
			ctl.NetConfig{Mode: modeTestMixed, Enabled: true})
	}})
}

func tone(k int) [160]int16 {
	var p [160]int16
	for i := range p {
		p[i] = int16(8000 * math.Sin(2*math.Pi*500*float64(k*160+i)/8000))
	}
	return p
}

func energy(p *[160]int16) (e float64) {
	for _, s := range p {
		e += float64(s) * float64(s)
	}
	return e / 160
}

// readPCM collects voice frames the engine sends to the USRP peer.
func readPCM(t *testing.T, g *rig, want int) (out [][160]int16) {
	for _, p := range collect(t, g.peer, want, 4*time.Second) {
		if p.p.Type == usrp.TypeVoice && p.p.PCM != nil {
			out = append(out, *p.p.PCM)
		}
	}
	return out
}

func TestIMBENetworkRX(t *testing.T) {
	g := testRig(t)
	n := testNetOf(t, modeTestIMBE)
	enc := imbe.NewCodec()
	s := n.sinkNow()
	s.Header(network.CallHeader{Call: 7, Src: "N0CALL", SrcID: 1234567, TG: 1998})
	const frames = 30
	for k := 0; k < frames; k++ {
		pcm := tone(k)
		f := enc.Encode(&pcm)
		s.Voice(network.VoiceFrame{Call: 7, Seq: uint32(k), Codec: network.CodecIMBE, Data: f[:]})
		time.Sleep(20 * time.Millisecond)
	}
	s.End(7)
	got := readPCM(t, g, frames)
	if len(got) != frames {
		t.Fatalf("got %d voice frames, want %d", len(got), frames)
	}
	loud := 0
	for i := range got {
		if energy(&got[i]) > 1e5 {
			loud++
		}
	}
	if loud < frames/2 {
		t.Fatalf("only %d of %d frames carry audio", loud, frames)
	}
	eventually(t, time.Second, func() bool { return g.ev.find(ctl.EvTalkEnd, "rx") != nil })
	if st := g.ev.find(ctl.EvTalkStart, "rx"); st == nil || st.Mode != modeTestIMBE || st.TG != 1998 {
		t.Fatalf("%+v", st)
	}
}

func TestIMBENetworkTX(t *testing.T) {
	g := testRig(t)
	n := testNetOf(t, modeTestIMBE)
	const frames = 25
	for k := 0; k < frames; k++ {
		p := usrp.Packet{Seq: uint32(k), Keyup: true, Type: usrp.TypeVoice, PCM: new([160]int16)}
		*p.PCM = tone(k)
		g.peer.WriteToUDP(p.Append(nil), g.engine)
		time.Sleep(20 * time.Millisecond)
	}
	g.peer.WriteToUDP(voice(frames, false), g.engine)
	eventually(t, 3*time.Second, func() bool { return len(n.sent()) == frames })
	dec := imbe.NewCodec()
	loud := 0
	for i, f := range n.sent() {
		if f.Codec != network.CodecIMBE || len(f.Data) != vocoder.IMBEBytes || f.Seq != uint32(i) {
			t.Fatalf("frame %d: %+v", i, f)
		}
		var b [vocoder.IMBEBytes]byte
		copy(b[:], f.Data)
		if pcm := dec.Decode(&b); energy(&pcm) > 1e5 {
			loud++
		}
	}
	if loud < frames/2 {
		t.Fatalf("only %d of %d frames decode to audio", loud, frames)
	}
	eventually(t, time.Second, func() bool { return g.ev.find(ctl.EvTalkEnd, "tx") != nil })
}

func TestMixedCodecRX(t *testing.T) {
	g := testRig(t)
	if _, err := g.r.Mode(context.Background(), ctl.ModeArgs{Mode: modeTestMixed}); err != nil {
		t.Fatal(err)
	}
	n := testNetOf(t, modeTestMixed)
	s := n.sinkNow()
	enc := imbe.NewCodec()
	// call 1: DN (AMBE, the zero Codec); the fake md380 decodes it
	s.Header(network.CallHeader{Call: 1, Src: "N0CALL"})
	for k := 0; k < 10; k++ {
		s.Voice(network.VoiceFrame{Call: 1, Seq: uint32(k), Data: make([]byte, vocoder.AMBEBytes)})
		time.Sleep(20 * time.Millisecond)
	}
	s.End(1)
	if got := readPCM(t, g, 10); len(got) != 10 {
		t.Fatalf("DN call: %d frames", len(got))
	}
	eventually(t, time.Second, func() bool { return g.ev.find(ctl.EvTalkEnd, "rx") != nil })
	// call 2: Voice FR (IMBE)
	s.Header(network.CallHeader{Call: 2, Src: "N0CALL"})
	for k := 0; k < 10; k++ {
		pcm := tone(k)
		f := enc.Encode(&pcm)
		s.Voice(network.VoiceFrame{Call: 2, Seq: uint32(k), Codec: network.CodecIMBE, Data: f[:]})
		time.Sleep(20 * time.Millisecond)
	}
	s.End(2)
	got := readPCM(t, g, 10)
	if len(got) != 10 {
		t.Fatalf("VW call: %d frames", len(got))
	}
	if energy(&got[8]) < 1e5 {
		t.Fatal("VW call decoded to silence")
	}
}

func TestWrongLengthDropped(t *testing.T) {
	g := testRig(t)
	n := testNetOf(t, modeTestIMBE)
	s := n.sinkNow()
	s.Header(network.CallHeader{Call: 3, Src: "N0CALL"})
	bad := []network.VoiceFrame{
		{Call: 3, Codec: network.CodecIMBE, Data: make([]byte, 7)},
		{Call: 3, Codec: network.CodecAMBE, Data: make([]byte, 11)},
		{Call: 3, Codec: network.CodecIMBE, Data: nil},
		{Call: 3, Codec: network.Codec(9), Data: make([]byte, 11)},
	}
	for _, f := range bad {
		s.Voice(f)
	}
	good := imbe.NewCodec().Encode(new([160]int16))
	s.Voice(network.VoiceFrame{Call: 3, Seq: 9, Codec: network.CodecIMBE, Data: good[:]})
	s.End(3)
	if got := readPCM(t, g, 1); len(got) != 1 {
		t.Fatalf("%d frames, want only the valid one", len(got))
	}
}
