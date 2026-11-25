package bridge_test

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/bridge"
	"github.com/artw1/aslcp/internal/dv/ctl"
	_ "github.com/artw1/aslcp/internal/dv/network/loopback"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
	"github.com/artw1/aslcp/internal/dv/vocoder/fake"
)

type events struct {
	mu  sync.Mutex
	evs []ctl.Event
}

func (e *events) add(ev ctl.Event) { e.mu.Lock(); e.evs = append(e.evs, ev); e.mu.Unlock() }

func (e *events) talks() (out []string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.evs {
		if ev.Talk != nil && (ev.Type == ctl.EvTalkStart || ev.Type == ctl.EvTalkEnd) {
			out = append(out, ev.Type+" "+ev.Talk.Dir)
		}
	}
	return
}

func (e *events) find(typ, dir string) *ctl.Talk {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, ev := range e.evs {
		if ev.Type == typ && ev.Talk != nil && ev.Talk.Dir == dir {
			return ev.Talk
		}
	}
	return nil
}

type rig struct {
	t      *testing.T
	r      *bridge.Router
	ep     *usrp.Endpoint
	md     *vocoder.MD380
	fake   *fake.Server
	peer   *net.UDPConn
	engine *net.UDPAddr
	ev     *events
}

const noMode = "-"

type rigOpts struct {
	timeout time.Duration
	cfg     func(*ctl.Config)
	mode    string
	txTO    time.Duration
	// loginWait is Options.LoginWait (0: the default).
	loginWait time.Duration
}

func newRig(t *testing.T, o rigOpts) *rig {
	t.Helper()
	if o.timeout == 0 {
		o.timeout = 300 * time.Millisecond
	}
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
	md := vocoder.NewMD380(srv.Addr(), o.timeout)
	t.Cleanup(func() { md.Close() })
	cfg := ctl.Config{
		Version:  1,
		Identity: ctl.Identity{Callsign: "N0CALL", DMRID: 1234567},
		Networks: []ctl.NetConfig{
			{Mode: ctl.ModeLoop, Enabled: true, Options: map[string]string{"playback_delay_ms": "10"}},
			{Mode: ctl.ModeBM, Enabled: true, Address: "192.0.2.10", Port: 62031},
		},
	}
	cfg.USRP.Listen, cfg.USRP.Peer = ep.Addr().String(), peer.LocalAddr().String()
	cfg.Vocoder.MD380 = srv.Addr()
	if o.cfg != nil {
		o.cfg(&cfg)
	}
	ev := &events{}
	mode := o.mode
	switch mode {
	case "":
		mode = ctl.ModeLoop
	case noMode:
		mode = ctl.ModeNone
	}
	r := bridge.New(bridge.Options{
		USRP: ep, AMBE: md, Config: cfg, Version: "test", VocoderStats: md.Stats,
		Emit: ev.add, InitialMode: mode, TxTimeout: o.txTO, LoginWait: o.loginWait,
	})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return &rig{t: t, r: r, ep: ep, md: md, fake: srv, peer: peer, engine: ep.Addr().(*net.UDPAddr), ev: ev}
}

func voice(k int, keyup bool) []byte {
	p := usrp.Packet{Seq: uint32(k), Keyup: keyup, Type: usrp.TypeVoice}
	if keyup {
		p.PCM = new([160]int16)
		for i := range p.PCM {
			p.PCM[i] = int16(1000 + k)
		}
	}
	return p.Append(nil)
}

func eventually(t *testing.T, d time.Duration, f func() bool) {
	t.Helper()
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if f() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}
