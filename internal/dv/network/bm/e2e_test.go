package bm

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/bridge"
	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
	"github.com/artw1/aslcp/internal/dv/vocoder/fake"
)

// TestEndToEndParrot runs the whole engine path against a fake master that
// echoes a call back like the BrandMeister parrot: USRP PCM in, fake md380
// encode, BM TX, master parrot echo, BM RX, md380 decode, USRP PCM out.
// It lives here because the fake master is private to this package's tests.
func TestEndToEndParrot(t *testing.T) {
	m := newMaster(t)
	m.mu.Lock()
	m.parrot = true
	m.mu.Unlock()

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

	opts := map[string]string{"client_version": "test 1.0"}
	for k, v := range fastOpts {
		opts[k] = v
	}
	opts["rx_timeout_ms"] = "400"
	cfg := ctl.Config{
		Version:  1,
		Identity: ctl.Identity{Callsign: "N0CALL", DMRID: testID},
		Networks: []ctl.NetConfig{{Mode: ctl.ModeBM, Enabled: true, Address: "127.0.0.1", Port: m.port(),
			Password: testPass, Options: opts}},
	}
	cfg.USRP.Listen, cfg.USRP.Peer = ep.Addr().String(), peer.LocalAddr().String()
	cfg.Vocoder.MD380 = srv.Addr()
	r := bridge.New(bridge.Options{USRP: ep, AMBE: md, Config: cfg, Version: "test", VocoderStats: md.Stats,
		Emit: func(ctl.Event) {}, InitialMode: ctl.ModeBM})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, "bm login", func() bool {
		st, err := r.Status(ctx)
		if err != nil {
			return false
		}
		for _, n := range st.Networks {
			if n.Mode == ctl.ModeBM && n.Login == ctl.LoginOK && n.Available {
				return true
			}
		}
		return false
	})
	if _, err := r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeBM, Target: ctl.Target{TG: 9990}}); err != nil {
		t.Fatal(err)
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
	last := usrp.Packet{Seq: n, Type: usrp.TypeVoice}
	peer.WriteToUDP(last.Append(nil), engine)

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
	if len(m.packets(typVoiceHeader)) == 0 || len(m.packets(typAudio)) == 0 {
		t.Fatalf("master saw no TX: %d voice headers, %d audio", len(m.packets(typVoiceHeader)), len(m.packets(typAudio)))
	}
	// The BM audio frame holds three AMBE frames (60 ms), so a whole number
	// of 3-frame groups comes back; allow the tail group to be short.
	if got < n-3 {
		t.Fatalf("parrot echoed %d of %d PCM frames", got, n)
	}
}

// A BM network without a master address is built-in but not usable: it stays
// available (the build has it) and the error says it is not configured.
func TestAvailableButNotConfigured(t *testing.T) {
	srv, err := fake.New("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { srv.Close() })
	peer, _ := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1")})
	t.Cleanup(func() { peer.Close() })
	ep, err := usrp.Listen("127.0.0.1:0", peer.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ep.Close() })
	md := vocoder.NewMD380(srv.Addr(), time.Second)
	t.Cleanup(func() { md.Close() })
	cfg := ctl.Config{Version: 1, Networks: []ctl.NetConfig{{Mode: ctl.ModeBM, Enabled: true}}}
	cfg.USRP.Listen, cfg.USRP.Peer = ep.Addr().String(), peer.LocalAddr().String()
	cfg.Vocoder.MD380 = srv.Addr()
	r := bridge.New(bridge.Options{USRP: ep, AMBE: md, Config: cfg, Version: "test", VocoderStats: md.Stats,
		Emit: func(ctl.Event) {}, InitialMode: ctl.ModeNone})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	st, err := r.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Networks) != 1 || !st.Networks[0].Available {
		t.Fatalf("%+v", st.Networks)
	}
	_, err = r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeBM})
	var ce *ctl.Error
	if !errors.As(err, &ce) || ce.Code != ctl.CodeNotEnabled {
		t.Fatalf("%v", err)
	}
}
