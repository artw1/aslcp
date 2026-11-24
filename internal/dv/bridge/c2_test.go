package bridge_test

import (
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/network"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

// modeTestC2 is a test-only network that carries Codec2 3200.
const modeTestC2 = "TC2"

func init() {
	network.Register(modeTestC2, func(ctl.NetConfig, ctl.Identity) (network.Network, error) {
		n := &testNet{mode: modeTestC2, codec: network.CodecC2}
		testNetsMu.Lock()
		testNets[modeTestC2] = n
		testNetsMu.Unlock()
		return n, nil
	})
}

func c2Rig(t *testing.T) *rig {
	testNetsMu.Lock()
	testNets = map[string]*testNet{}
	testNetsMu.Unlock()
	return newRig(t, rigOpts{mode: modeTestC2, cfg: func(c *ctl.Config) {
		c.Networks = append(c.Networks, ctl.NetConfig{Mode: modeTestC2, Enabled: true})
	}})
}

func TestC2NetworkRX(t *testing.T) {
	g := c2Rig(t)
	n := testNetOf(t, modeTestC2)
	enc := vocoder.NewCodec2()
	s := n.sinkNow()
	s.Header(network.CallHeader{Call: 5, Src: "N0CALL"})
	const frames = 30
	for k := 0; k < frames; k++ {
		pcm := tone(k)
		f := enc.Encode(&pcm)
		s.Voice(network.VoiceFrame{Call: 5, Seq: uint32(k), Codec: network.CodecC2, Data: f[:]})
		time.Sleep(20 * time.Millisecond)
	}
	s.End(5)
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
}

func TestC2NetworkTX(t *testing.T) {
	g := c2Rig(t)
	n := testNetOf(t, modeTestC2)
	const frames = 25
	for k := 0; k < frames; k++ {
		p := usrp.Packet{Seq: uint32(k), Keyup: true, Type: usrp.TypeVoice, PCM: new([160]int16)}
		*p.PCM = tone(k)
		g.peer.WriteToUDP(p.Append(nil), g.engine)
		time.Sleep(20 * time.Millisecond)
	}
	g.peer.WriteToUDP(voice(frames, false), g.engine)
	eventually(t, 3*time.Second, func() bool { return len(n.sent()) == frames })
	dec := vocoder.NewCodec2()
	loud := 0
	for i, f := range n.sent() {
		if f.Codec != network.CodecC2 || len(f.Data) != vocoder.C2Bytes || f.Seq != uint32(i) {
			t.Fatalf("frame %d: %+v", i, f)
		}
		var b [vocoder.C2Bytes]byte
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

// TestC2Loopback is PCM -> Codec2 -> PCM through the whole bridge: the node
// transmits on the C2 network, the test network hands the frames back as a
// received call, and the audio returns on USRP.
func TestC2Loopback(t *testing.T) {
	g := c2Rig(t)
	n := testNetOf(t, modeTestC2)
	const frames = 25
	for k := 0; k < frames; k++ {
		p := usrp.Packet{Seq: uint32(k), Keyup: true, Type: usrp.TypeVoice, PCM: new([160]int16)}
		*p.PCM = tone(k)
		g.peer.WriteToUDP(p.Append(nil), g.engine)
		time.Sleep(20 * time.Millisecond)
	}
	g.peer.WriteToUDP(voice(frames, false), g.engine)
	eventually(t, 3*time.Second, func() bool { return len(n.sent()) == frames })
	eventually(t, time.Second, func() bool { return g.ev.find(ctl.EvTalkEnd, "tx") != nil })
	s := n.sinkNow()
	s.Header(network.CallHeader{Call: 9, Src: "N0CALL"})
	for _, f := range n.sent() {
		f.Call = 9
		s.Voice(f)
	}
	s.End(9)
	got := readPCM(t, g, frames)
	if len(got) != frames {
		t.Fatalf("got %d frames, want %d", len(got), frames)
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
}

func TestC2WrongLengthDropped(t *testing.T) {
	g := c2Rig(t)
	n := testNetOf(t, modeTestC2)
	s := n.sinkNow()
	s.Header(network.CallHeader{Call: 4, Src: "N0CALL"})
	for _, f := range []network.VoiceFrame{
		{Call: 4, Codec: network.CodecC2, Data: make([]byte, 7)},
		{Call: 4, Codec: network.CodecC2, Data: make([]byte, 11)},
		{Call: 4, Codec: network.CodecAMBE, Data: make([]byte, 8)},
	} {
		s.Voice(f)
	}
	s.Voice(network.VoiceFrame{Call: 4, Seq: 9, Codec: network.CodecC2, Data: make([]byte, vocoder.C2Bytes)})
	s.End(4)
	if got := readPCM(t, g, 1); len(got) != 1 {
		t.Fatalf("%d frames, want only the valid one", len(got))
	}
}
