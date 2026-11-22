package bridge_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/network"
)

func code(err error) string {
	var ce *ctl.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	return ""
}

func TestModeErrorsAndTune(t *testing.T) {
	g := newRig(t, rigOpts{mode: noMode})
	ctx := context.Background()
	if _, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeBM}); code(err) != ctl.CodeNotAvailable {
		t.Fatalf("BM: %v", err)
	}
	if _, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeYSF}); code(err) != ctl.CodeNotAvailable {
		t.Fatalf("YSF: %v", err)
	}
	st, err := g.r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeLoop, Target: ctl.Target{TG: 9990}})
	if err != nil || st.Mode != ctl.ModeLoop || st.Target == nil || st.Target.TG != 9990 {
		t.Fatalf("%+v %v", st, err)
	}
	if len(st.Networks) != 2 || !st.Networks[0].Available || st.Networks[1].Available {
		t.Fatalf("%+v", st.Networks)
	}
	st, err = g.r.Unlink(ctx, ctl.UnlinkArgs{})
	if err != nil || st.Target != nil {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := g.r.Unlink(ctx, ctl.UnlinkArgs{Mode: ctl.ModeBM}); code(err) != ctl.CodeBadRequest {
		t.Fatalf("%v", err)
	}
	if st, err = g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeNone}); err != nil || st.Mode != "" {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestNotEnabled(t *testing.T) {
	g := newRig(t, rigOpts{cfg: func(c *ctl.Config) { c.Networks[0].Enabled = false }, mode: noMode})
	if _, err := g.r.Mode(context.Background(), ctl.ModeArgs{Mode: ctl.ModeLoop}); code(err) != ctl.CodeNotEnabled {
		t.Fatalf("%v", err)
	}
}

func TestBusyAndForce(t *testing.T) {
	g := newRig(t, rigOpts{})
	g.peer.WriteToUDP(voice(0, true), g.engine)
	eventually(t, 2*time.Second, func() bool {
		st, _ := g.r.Status(context.Background())
		return st.Talk != nil
	})
	ctx := context.Background()
	if _, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeNone}); code(err) != ctl.CodeBusy {
		t.Fatalf("%v", err)
	}
	st, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeNone, Force: true})
	if err != nil || st.Talk != nil || st.Mode != "" {
		t.Fatalf("%+v %v", st, err)
	}
}

func TestTxTimeoutGuard(t *testing.T) {
	g := newRig(t, rigOpts{})
	for k := 0; k < 3; k++ {
		g.peer.WriteToUDP(voice(k, true), g.engine)
		time.Sleep(20 * time.Millisecond)
	}
	// No keyup=0: the guard ends the call.
	eventually(t, 3*time.Second, func() bool { return g.ev.find("talk_end", "tx") != nil })
}

func TestNoModeIgnoresTx(t *testing.T) {
	g := newRig(t, rigOpts{mode: noMode})
	g.peer.WriteToUDP(voice(0, true), g.engine)
	time.Sleep(200 * time.Millisecond)
	if got := g.ev.talks(); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestVocoderStatus(t *testing.T) {
	g := newRig(t, rigOpts{})
	st, _ := g.r.Status(context.Background())
	if !st.Vocoder.OK || st.Vocoder.Addr != g.fake.Addr() || st.USRP.Peer == "" || st.Version != "test" {
		t.Fatalf("%+v", st)
	}
}

// ---- reload and the rx silence guard use a scripted network ----

type scripted struct {
	mu   sync.Mutex
	sink network.Sink
	up   chan struct{}
}

func (s *scripted) Mode() string                           { return ctl.ModeP25 }
func (s *scripted) Codec() network.Codec                   { return network.CodecAMBE }
func (s *scripted) Tune(context.Context, ctl.Target) error { return nil }
func (s *scripted) Unlink(context.Context) error           { return nil }
func (s *scripted) StartTX(network.CallHeader) error       { return nil }
func (s *scripted) WriteVoice(network.VoiceFrame) error    { return nil }
func (s *scripted) EndTX(uint64) error                     { return nil }
func (s *scripted) Status() ctl.NetStatus                  { return ctl.NetStatus{Mode: ctl.ModeP25, Available: true} }
func (s *scripted) Run(ctx context.Context, k network.Sink) error {
	s.mu.Lock()
	s.sink = k
	s.mu.Unlock()
	close(s.up)
	<-ctx.Done()
	return nil
}

var (
	regOnce sync.Once
	curNet  atomic.Pointer[scripted]
)

func TestRxSilenceGuard(t *testing.T) {
	sn := &scripted{up: make(chan struct{})}
	curNet.Store(sn)
	regOnce.Do(func() {
		network.Register(ctl.ModeP25, func(ctl.NetConfig, ctl.Identity) (network.Network, error) { return curNet.Load(), nil })
	})
	g := newRig(t, rigOpts{
		mode: ctl.ModeP25,
		cfg: func(c *ctl.Config) {
			c.Networks = append(c.Networks, ctl.NetConfig{Mode: ctl.ModeP25, Enabled: true})
		},
	})
	<-sn.up
	sn.sink.Header(network.CallHeader{Call: 9, Src: "N0CALL", Room: "00001"})
	sn.sink.Voice(network.VoiceFrame{Call: 9, Data: make([]byte, 7)})
	eventually(t, 2*time.Second, func() bool { return g.ev.find("talk_start", "rx") != nil })
	// No terminator: the 1.5 s guard ends it.
	eventually(t, 4*time.Second, func() bool { return g.ev.find("talk_end", "rx") != nil })
	if tk := g.ev.find("talk_start", "rx"); tk.Room != "00001" || tk.Call != "N0CALL" {
		t.Fatalf("%+v", tk)
	}
}
