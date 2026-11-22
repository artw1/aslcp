package usrp

import (
	"net"
	"testing"
	"time"
)

func peerConn(t *testing.T, ip string) *net.UDPConn {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP(ip)})
	if err != nil {
		t.Skipf("cannot bind %s: %v", ip, err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestEndpointRxFilterAndTx(t *testing.T) {
	peer := peerConn(t, "127.0.0.1")
	ep, err := Listen("127.0.0.1:0", peer.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	dst := ep.Addr().(*net.UDPAddr)

	// A foreign source is ignored, the peer is accepted.
	other := peerConn(t, "127.0.0.2")
	var pcm [Samples]int16
	pcm[0] = 1234
	pkt := (&Packet{Seq: 1, Keyup: true, Type: TypeVoice, PCM: &pcm}).Append(nil)
	other.WriteToUDP(pkt, dst)
	peer.WriteToUDP([]byte("garbage"), dst)
	peer.WriteToUDP(pkt, dst)
	select {
	case p := <-ep.Rx():
		if p.PCM == nil || p.PCM[0] != 1234 {
			t.Fatalf("bad packet %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no rx")
	}
	select {
	case p := <-ep.Rx():
		t.Fatalf("unexpected packet %+v", p)
	case <-time.After(100 * time.Millisecond):
	}
	if s := ep.Stats(); s.Foreign != 2 || s.RxFrames != 1 || s.LastRx.IsZero() {
		t.Fatalf("stats %+v", s)
	}

	// Paced tx: 5 frames then end.
	for i := 0; i < 5; i++ {
		var f [Samples]int16
		f[0] = int16(100 + i)
		ep.SendVoice(f)
	}
	ep.EndTX()
	buf := make([]byte, 2048)
	var times []time.Time
	for i := 0; i < 6; i++ {
		peer.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _, err := peer.ReadFromUDP(buf)
		if err != nil {
			t.Fatal(err)
		}
		times = append(times, time.Now())
		p, err := Parse(buf[:n])
		if err != nil {
			t.Fatal(err)
		}
		if p.Seq != uint32(i) {
			t.Errorf("seq %d want %d", p.Seq, i)
		}
		if i < 5 && (!p.Keyup || p.PCM == nil || p.PCM[0] != int16(100+i)) {
			t.Fatalf("frame %d: %+v", i, p)
		}
		if i == 5 && (p.Keyup || p.PCM != nil) {
			t.Fatalf("end: %+v", p)
		}
	}
	if d := times[5].Sub(times[0]); d < 60*time.Millisecond || d > 400*time.Millisecond {
		t.Errorf("5 intervals took %v", d)
	}
	if err := ep.SendText(MetaTLV("PARROT 9990")); err != nil {
		t.Fatal(err)
	}
	peer.SetReadDeadline(time.Now().Add(2 * time.Second))
	n, _, _ := peer.ReadFromUDP(buf)
	p, err := Parse(buf[:n])
	if err != nil || p.Type != TypeText {
		t.Fatalf("%+v %v", p, err)
	}
	if v, ok := ParseTLV(p.Text, TLVSetInfo); !ok || string(v) != "PARROT 9990" {
		t.Fatalf("%q", v)
	}
}

func TestEndpointTxQueueCap(t *testing.T) {
	peer := peerConn(t, "127.0.0.1")
	ep, err := Listen("127.0.0.1:0", peer.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer ep.Close()
	// Queue far more than the cap in one burst; the pacer drains at most a
	// handful meanwhile.
	for i := 0; i < TxQueueMax+20; i++ {
		var f [Samples]int16
		ep.SendVoice(f)
	}
	if q := ep.Queued(); q > TxQueueMax {
		t.Fatalf("queue %d > cap", q)
	}
	if s := ep.Stats(); s.TxDropped < 10 {
		t.Fatalf("dropped %d", s.TxDropped)
	}
}
