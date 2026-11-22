package homebrew

import (
	"context"
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
// echoes DMRD packets back like a parrot talkgroup: USRP PCM in, fake md380
// encode, DMR framing, DMRD TX, master echo, DMRD RX, md380 decode, USRP PCM out.
func TestEndToEndParrot(t *testing.T) {
	for _, mode := range []string{ctl.ModeTGIF, ctl.ModeDMR} {
		t.Run(mode, func(t *testing.T) { endToEndParrot(t, mode) })
	}
}

func endToEndParrot(t *testing.T, mode string) {
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

	opts := fastOpts(map[string]string{"rx_timeout_ms": "400", "login_timeout_ms": "500", "ping_interval_ms": "1000"})
	cfg := ctl.Config{
		Version:  1,
		Identity: testID,
		Networks: []ctl.NetConfig{{Mode: mode, Enabled: true, Address: "127.0.0.1", Port: m.port(), Password: testPW, Options: opts}},
	}
	cfg.USRP.Listen, cfg.USRP.Peer = ep.Addr().String(), peer.LocalAddr().String()
	cfg.Vocoder.MD380 = srv.Addr()
	r := bridge.New(bridge.Options{USRP: ep, AMBE: md, Config: cfg, Version: "test", VocoderStats: md.Stats,
		Emit: func(ctl.Event) {}, InitialMode: mode})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	waitFor(t, mode+" login", func() bool {
		st, err := r.Status(ctx)
		if err != nil {
			return false
		}
		for _, n := range st.Networks {
			if n.Mode == mode && n.Login == ctl.LoginOK && n.Available {
				return true
			}
		}
		return false
	})
	if _, err := r.Tune(ctx, ctl.TuneArgs{Mode: mode, Target: ctl.Target{TG: 1997}}); err != nil {
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
	var voice, term int
	for _, d := range dmrdTo(m, 0) {
		if d.Dst != 1997 || d.Slot != 2 {
			continue
		}
		if d.FrameTyp == ftVoiceSync || d.FrameTyp == ftVoice {
			voice++
		}
		if d.FrameTyp == ftDataSync && d.DType == dtTerminator {
			term++
		}
	}
	if voice == 0 || term == 0 {
		t.Fatalf("master saw %d voice bursts, %d terminators", voice, term)
	}
	// three md380 frames per burst; the tail group may be short
	if got < n-6 {
		t.Fatalf("parrot echoed %d of %d PCM frames", got, n)
	}
}
