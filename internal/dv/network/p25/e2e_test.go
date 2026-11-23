// SPDX-License-Identifier: GPL-3.0-or-later

package p25

import (
	"context"
	"math"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/bridge"
	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
	"github.com/artw1/aslcp/internal/dv/vocoder/fake"
)

// echoRef is a reflector that echoes polls and, once a transmission ends
// (the 0x80 terminator), plays every packet of it back, like a parrot room.
type echoRef struct {
	conn *net.UDPConn
	mu   sync.Mutex
	data int
	held [][]byte
}

func newEchoRef(t *testing.T) *echoRef {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	r := &echoRef{conn: c}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 2048)
		for {
			l, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p := buf[:l]
			switch {
			case l == 11 && p[0] == 0xF0:
				c.WriteToUDP(p, from)
			case l > 1 && p[0] >= 0x62:
				r.mu.Lock()
				r.data++
				r.held = append(r.held, append([]byte(nil), p...))
				var play [][]byte
				if p[0] == 0x80 {
					play, r.held = r.held, nil
				}
				r.mu.Unlock()
				for _, q := range play {
					c.WriteToUDP(q, from)
					time.Sleep(10 * time.Millisecond)
				}
			}
		}
	}()
	return r
}

func (r *echoRef) seen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data
}

func energy(p *[160]int16) (e float64) {
	for _, s := range p {
		e += float64(s) * float64(s)
	}
	return e / 160
}

// TestEndToEndParrot runs the engine path against a reflector that echoes:
// USRP PCM in, IMBE encode, P25 LDU records, reflector echo after the
// terminator, P25 RX, IMBE decode, USRP PCM out.
func TestEndToEndParrot(t *testing.T) {
	ref := newEchoRef(t)
	srv, err := fake.New("127.0.0.1:0") // md380 is not used on P25, but the router needs one
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	peer, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { peer.Close() })
	ep, err := usrp.Listen("127.0.0.1:0", peer.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ep.Close() })
	md := vocoder.NewMD380(srv.Addr(), 500*time.Millisecond)
	t.Cleanup(func() { md.Close() })

	cfg := ctl.Config{
		Version:  1,
		Identity: ctl.Identity{Callsign: "N0CALL", DMRID: 1234567},
		Networks: []ctl.NetConfig{{Mode: ctl.ModeP25, Enabled: true,
			Options: map[string]string{"rx_timeout_ms": "400", "poll_ms": "1000", "tick_ms": "20"}}},
	}
	cfg.USRP.Listen, cfg.USRP.Peer = ep.Addr().String(), peer.LocalAddr().String()
	cfg.Vocoder.MD380 = srv.Addr()
	r := bridge.New(bridge.Options{USRP: ep, AMBE: md, Config: cfg, Version: "test", VocoderStats: md.Stats,
		Emit: func(ctl.Event) {}, InitialMode: ctl.ModeP25})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	if _, err := r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeP25, Target: ctl.Target{Room: "1998", Host: "127.0.0.1", Port: ref.conn.LocalAddr().(*net.UDPAddr).Port}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := r.Status(ctx)
		if err == nil {
			ok := false
			for _, n := range st.Networks {
				ok = ok || n.Mode == ctl.ModeP25 && n.Login == ctl.LoginOK
			}
			if ok {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("P25 link did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	engine := ep.Addr().(*net.UDPAddr)
	const n = 40
	for k := 0; k < n; k++ {
		p := usrp.Packet{Seq: uint32(k), Keyup: true, Type: usrp.TypeVoice, PCM: new([160]int16)}
		for i := range p.PCM {
			p.PCM[i] = int16(8000 * math.Sin(2*math.Pi*500*float64(k*160+i)/8000))
		}
		peer.WriteToUDP(p.Append(nil), engine)
		time.Sleep(20 * time.Millisecond)
	}
	peer.WriteToUDP((&usrp.Packet{Seq: n, Type: usrp.TypeVoice}).Append(nil), engine)

	got, loud := 0, 0
	buf := make([]byte, 2048)
	end := time.Now().Add(6 * time.Second)
	for time.Now().Before(end) && got < n {
		peer.SetReadDeadline(end)
		l, _, err := peer.ReadFromUDP(buf)
		if err != nil {
			break
		}
		p, err := usrp.Parse(buf[:l])
		if err != nil {
			t.Fatal(err)
		}
		if p.Type == usrp.TypeVoice && p.PCM != nil {
			got++
			if energy(p.PCM) > 1e5 {
				loud++
			}
		}
	}
	// 40 frames = 4 LDUs of nine records each (the last padded) + terminator
	if ref.seen() < 36 {
		t.Fatalf("reflector saw %d P25 packets", ref.seen())
	}
	if got < n-5 {
		t.Fatalf("parrot echoed %d of %d PCM frames", got, n)
	}
	if loud < n/2 {
		t.Fatalf("only %d frames carry audio", loud)
	}
}
