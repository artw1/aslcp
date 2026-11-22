// SPDX-License-Identifier: GPL-3.0-or-later

package ysf

import (
	"context"
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

// echoRef is a reflector that answers polls and, once a transmission ends,
// plays every YSFD packet of it back, like a parrot room.
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
			case l >= 4 && string(p[:4]) == "YSFP":
				c.WriteToUDP([]byte("YSFPECHO TEST "), from)
			case l == PacketLen && string(p[:4]) == "YSFD":
				r.mu.Lock()
				r.data++
				r.held = append(r.held, append([]byte(nil), p...))
				var play [][]byte
				if p[34]&1 != 0 {
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

// TestEndToEndParrot runs the engine path against a reflector that echoes:
// USRP PCM in, fake md380 encode, YSF V/D mode 2 framing, reflector echo,
// YSF RX, md380 decode, USRP PCM out.
func TestEndToEndParrot(t *testing.T) {
	ref := newEchoRef(t)
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
		Identity: ctl.Identity{Callsign: "N0CALL", DMRID: 1234567},
		Networks: []ctl.NetConfig{{Mode: ctl.ModeYSF, Enabled: true,
			Options: map[string]string{"rx_timeout_ms": "400", "poll_ms": "1000", "tick_ms": "20"}}},
	}
	cfg.USRP.Listen, cfg.USRP.Peer = ep.Addr().String(), peer.LocalAddr().String()
	cfg.Vocoder.MD380 = srv.Addr()
	r := bridge.New(bridge.Options{USRP: ep, AMBE: md, Config: cfg, Version: "test", VocoderStats: md.Stats,
		Emit: func(ctl.Event) {}, InitialMode: ctl.ModeYSF})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	if _, err := r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeYSF, Target: ctl.Target{Room: "12345", Host: "127.0.0.1", Port: ref.conn.LocalAddr().(*net.UDPAddr).Port}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, err := r.Status(ctx)
		if err == nil {
			ok := false
			for _, n := range st.Networks {
				ok = ok || n.Mode == ctl.ModeYSF && n.Login == ctl.LoginOK
			}
			if ok {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("YSF link did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}

	engine := ep.Addr().(*net.UDPAddr)
	const n = 30
	for k := 0; k < n; k++ {
		p := usrp.Packet{Seq: uint32(k), Keyup: true, Type: usrp.TypeVoice, PCM: new([160]int16)}
		for i := range p.PCM {
			p.PCM[i] = int16(1000 + k)
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
	// header + 6 communications frames + terminator
	if ref.seen() < 8 {
		t.Fatalf("reflector saw %d YSFD packets", ref.seen())
	}
	// five md380 frames per YSF frame; the tail group may be short
	if got < n-10 {
		t.Fatalf("parrot echoed %d of %d PCM frames", got, n)
	}
}
