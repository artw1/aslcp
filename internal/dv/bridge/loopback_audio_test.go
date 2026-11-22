package bridge_test

import (
	"net"
	"reflect"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

type rxPkt struct {
	at time.Time
	p  usrp.Packet
}

// sendCall plays n frames at 20 ms then a bare keyup=0 header from src.
func sendCall(t *testing.T, src *net.UDPConn, dst *net.UDPAddr, n int) {
	for k := 0; k < n; k++ {
		src.WriteToUDP(voice(k, true), dst)
		time.Sleep(20 * time.Millisecond)
	}
	src.WriteToUDP(voice(n, false), dst)
}

// collect reads packets until the keyup=0 after the voice, or the deadline.
func collect(t *testing.T, peer *net.UDPConn, wantVoice int, d time.Duration) []rxPkt {
	var out []rxPkt
	buf := make([]byte, 2048)
	end := time.Now().Add(d)
	voiceSeen := 0
	for time.Now().Before(end) {
		peer.SetReadDeadline(end)
		n, _, err := peer.ReadFromUDP(buf)
		if err != nil {
			break
		}
		p, err := usrp.Parse(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rxPkt{time.Now(), p})
		if p.Type == usrp.TypeVoice && p.PCM != nil {
			voiceSeen++
		}
		if voiceSeen >= wantVoice && p.Type == usrp.TypeVoice && !p.Keyup {
			break
		}
	}
	return out
}

func TestLoopbackAudio(t *testing.T) {
	g := newRig(t, rigOpts{})
	sendCall(t, g.peer, g.engine, 50)
	pk := collect(t, g.peer, 50, 4*time.Second)

	var frames []rxPkt
	var text, last *rxPkt
	for i := range pk {
		p := pk[i]
		switch {
		case p.p.Type == usrp.TypeText:
			text = &pk[i]
		case p.p.Type == usrp.TypeVoice && p.p.PCM != nil:
			frames = append(frames, p)
		case p.p.Type == usrp.TypeVoice && !p.p.Keyup:
			last = &pk[i]
		}
	}
	if len(frames) != 50 {
		t.Fatalf("%d voice frames", len(frames))
	}
	for k, f := range frames {
		if !f.p.Keyup || f.p.PCM[0] != int16(1000+k) || f.p.PCM[159] != int16(1000+k) {
			t.Fatalf("frame %d: %d", k, f.p.PCM[0])
		}
		if k > 0 {
			if gap := f.at.Sub(frames[k-1].at); gap > 120*time.Millisecond {
				t.Errorf("gap before frame %d: %v", k, gap)
			}
		}
	}
	if span := frames[49].at.Sub(frames[0].at); span < 800*time.Millisecond || span > 1300*time.Millisecond {
		t.Errorf("playback spans %v, want about 0.98 s", span)
	}
	if last == nil {
		t.Fatal("no final keyup=0")
	}
	if text == nil {
		t.Fatal("no TEXT packet")
	}
	if v, ok := usrp.ParseTLV(text.p.Text, usrp.TLVSetInfo); !ok || string(v) != "PARROT 9990" {
		t.Fatalf("text %q", v)
	}

	eventually(t, 2*time.Second, func() bool { return len(g.ev.talks()) >= 4 })
	want := []string{"talk_start tx", "talk_end tx", "talk_start rx", "talk_end rx"}
	if got := g.ev.talks(); !reflect.DeepEqual(got, want) {
		t.Fatalf("events %v", got)
	}
	end := g.ev.find("talk_end", "rx")
	if end.Duration < 700*time.Millisecond || end.Duration > 1600*time.Millisecond || end.Call != "PARROT" || end.TG != 9990 {
		t.Fatalf("rx talk %+v", end)
	}
	txEnd := g.ev.find("talk_end", "tx")
	if txEnd.Call != "N0CALL" || txEnd.DMRID != 1234567 {
		t.Fatalf("tx talk %+v", txEnd)
	}
}

func TestLoopbackAudioDroppedFrame(t *testing.T) {
	g := newRig(t, rigOpts{timeout: 100 * time.Millisecond})
	// Request 5 is the encode of frame 4.
	g.fake.SetDrop(func(n uint64) bool { return n == 5 })
	sendCall(t, g.peer, g.engine, 20)
	pk := collect(t, g.peer, 20, 4*time.Second)
	var frames []rxPkt
	for _, p := range pk {
		if p.p.Type == usrp.TypeVoice && p.p.PCM != nil {
			frames = append(frames, p)
		}
	}
	if len(frames) != 20 {
		t.Fatalf("%d frames", len(frames))
	}
	silence := int16(uint16(vocoder.SilenceAMBE[1])<<8 | uint16(vocoder.SilenceAMBE[2]))
	for k, f := range frames {
		want := int16(1000 + k)
		if k == 4 {
			want = silence
		}
		if f.p.PCM[0] != want {
			t.Fatalf("frame %d = %d, want %d", k, f.p.PCM[0], want)
		}
	}
	if s := g.md.Stats(); s.Timeouts != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestForeignSourceIgnored(t *testing.T) {
	g := newRig(t, rigOpts{})
	other, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.2")})
	if err != nil {
		t.Skipf("no 127.0.0.2: %v", err)
	}
	defer other.Close()
	sendCall(t, other, g.engine, 10)
	time.Sleep(300 * time.Millisecond)
	if got := g.ev.talks(); len(got) != 0 {
		t.Fatalf("events %v", got)
	}
	if g.ep.Stats().Foreign < 10 {
		t.Fatalf("%+v", g.ep.Stats())
	}
}
