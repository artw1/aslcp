// SPDX-License-Identifier: GPL-3.0-or-later

package m17

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/bridge"
	"github.com/artw1/aslcp/internal/dv/ctl"
	proto "github.com/artw1/aslcp/internal/dv/m17"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
	"github.com/artw1/aslcp/internal/dv/vocoder/fake"
)

// parrotRef acks links, and plays a stream back once its last packet arrived.
type parrotRef struct {
	conn *net.UDPConn
	mu   sync.Mutex
	data int
}

func newParrot(t *testing.T) *parrotRef {
	t.Helper()
	c, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	r := &parrotRef{conn: c}
	t.Cleanup(func() { c.Close() })
	go func() {
		buf := make([]byte, 2048)
		var held [][]byte
		for {
			l, from, err := c.ReadFromUDP(buf)
			if err != nil {
				return
			}
			p := append([]byte(nil), buf[:l]...)
			switch {
			case l == proto.ConnLen && string(p[:4]) == "CONN":
				c.WriteToUDP([]byte("ACKN"), from)
			case l == proto.StreamLen && proto.IsStream(p):
				s, err := proto.UnmarshalStream(p)
				if err != nil {
					continue
				}
				r.mu.Lock()
				r.data++
				r.mu.Unlock()
				held = append(held, p)
				if s.Last() {
					for _, q := range held {
						c.WriteToUDP(q, from)
						time.Sleep(5 * time.Millisecond)
					}
					held = nil
				}
			}
		}
	}()
	return r
}

func (r *parrotRef) seen() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.data
}

// TestEndToEndParrot: USRP PCM in, Codec2 encode, M17 packets, reflector
// echo, M17 RX, Codec2 decode, USRP PCM out.
func TestEndToEndParrot(t *testing.T) {
	ref := newParrot(t)
	srv, err := fake.New("127.0.0.1:0")
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
		Identity: ctl.Identity{Callsign: "N0CALL"},
		// A lost CONN/ACKN on a busy runner must be retried well within the deadline.
		Networks: []ctl.NetConfig{{Mode: ctl.ModeM17, Enabled: true,
			Options: map[string]string{"rx_timeout_ms": "400", "tick_ms": "20", "ack_timeout_ms": "200"}}},
	}
	cfg.USRP.Listen, cfg.USRP.Peer = ep.Addr().String(), peer.LocalAddr().String()
	cfg.Vocoder.MD380 = srv.Addr()
	r := bridge.New(bridge.Options{USRP: ep, AMBE: md, Config: cfg, Version: "test", VocoderStats: md.Stats,
		Emit: func(ctl.Event) {}, InitialMode: ctl.ModeM17})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	if _, err := r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeM17, Target: ctl.Target{Room: "M17-XXX C", Host: "127.0.0.1", Port: ref.conn.LocalAddr().(*net.UDPAddr).Port}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, err := r.Status(ctx)
		if err == nil {
			ok := false
			for _, n := range st.Networks {
				ok = ok || n.Mode == ctl.ModeM17 && n.Login == ctl.LoginOK
			}
			if ok {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("M17 link did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	engine := ep.Addr().(*net.UDPAddr)
	const n = 30
	for k := 0; k < n; k++ {
		p := usrp.Packet{Seq: uint32(k), Keyup: true, Type: usrp.TypeVoice, PCM: new([160]int16)}
		for i := range p.PCM {
			p.PCM[i] = int16(3000 * (1 - 2*((i/20+k)%2)))
		}
		peer.WriteToUDP(p.Append(nil), engine)
		time.Sleep(20 * time.Millisecond)
	}
	peer.WriteToUDP((&usrp.Packet{Seq: n, Type: usrp.TypeVoice}).Append(nil), engine)

	got := 0
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
		}
	}
	if ref.seen() < n/2-2 {
		t.Fatalf("reflector saw %d stream packets", ref.seen())
	}
	if got < n-4 {
		t.Fatalf("parrot echoed %d of %d PCM frames", got, n)
	}
}
