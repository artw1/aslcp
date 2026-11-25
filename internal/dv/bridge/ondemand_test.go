package bridge_test

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/network"
)

// odHub stands in for an on-demand network such as BrandMeister: every session
// is a new odNet, and the hub records what the router did to it, in order.
type odHub struct {
	mu       sync.Mutex
	log      []string
	behavior string // "ok", "refuse" or "silent" (never answers the login)
	nets     int
	running  int
}

func (h *odHub) add(s string) { h.mu.Lock(); h.log = append(h.log, s); h.mu.Unlock() }

func (h *odHub) entries() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return strings.Join(h.log, ",")
}

func (h *odHub) up() int { h.mu.Lock(); defer h.mu.Unlock(); return h.running }

type odNet struct {
	h      *odHub
	logged bool
}

func (n *odNet) Mode() string                        { return ctl.ModeNXDN }
func (n *odNet) Codec() network.Codec                { return network.CodecAMBE }
func (n *odNet) StartTX(network.CallHeader) error    { return nil }
func (n *odNet) WriteVoice(network.VoiceFrame) error { return nil }
func (n *odNet) EndTX(uint64) error                  { return nil }
func (n *odNet) Status() ctl.NetStatus               { return ctl.NetStatus{Mode: ctl.ModeNXDN, Available: true} }
func (n *odNet) Tune(_ context.Context, t ctl.Target) error {
	n.h.add("tune")
	return nil
}
func (n *odNet) Unlink(context.Context) error { n.h.add("unlink"); return nil }
func (n *odNet) Run(ctx context.Context, s network.Sink) error {
	n.h.mu.Lock()
	n.h.running++
	b := n.h.behavior
	n.h.mu.Unlock()
	n.h.add("run")
	s.Login(ctl.LoginPending, "")
	switch b {
	case "ok":
		s.Login(ctl.LoginOK, "")
	case "refuse":
		s.Login(ctl.LoginFailed, "authentication refused")
	}
	<-ctx.Done()
	n.h.add("close") // the close packet of a real session
	n.h.mu.Lock()
	n.h.running--
	n.h.mu.Unlock()
	return nil
}

var (
	odOnce sync.Once
	odCur  = &odHub{}
	odMu   sync.Mutex // one test at a time owns the hub
)

// odRig starts a router with the fake on-demand network (registered as NXDN,
// which no other test of this package uses). onDemand false makes it
// always-on, like TGIF.
func odRig(t *testing.T, behavior string, onDemand bool, mode string, wait time.Duration) (*rig, *odHub) {
	t.Helper()
	odOnce.Do(func() {
		network.Register(ctl.ModeNXDN, func(ctl.NetConfig, ctl.Identity) (network.Network, error) {
			odMu.Lock()
			h := odCur
			odMu.Unlock()
			h.mu.Lock()
			h.nets++
			h.mu.Unlock()
			return &odNet{h: h}, nil
		})
	})
	h := &odHub{behavior: behavior}
	odMu.Lock()
	odCur = h
	odMu.Unlock()
	g := newRig(t, rigOpts{mode: mode, loginWait: wait, cfg: func(c *ctl.Config) {
		c.Networks = []ctl.NetConfig{
			c.Networks[0],
			{Mode: ctl.ModeNXDN, Enabled: true, OnDemand: &onDemand},
		}
	}})
	return g, h
}

func netLogin(t *testing.T, g *rig) ctl.NetStatus {
	t.Helper()
	st, err := g.r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range st.Networks {
		if n.Mode == ctl.ModeNXDN {
			return n
		}
	}
	t.Fatal("no NXDN network")
	return ctl.NetStatus{}
}

func TestOnDemandEnterTunesAfterLoginAndLeaveStops(t *testing.T) {
	g, h := odRig(t, "ok", true, noMode, 0)
	ctx := context.Background()
	if n := netLogin(t, g); n.Login != ctl.LoginIdle {
		t.Fatalf("before: %+v", n)
	}
	if h.entries() != "" {
		t.Fatalf("a session started with the engine: %s", h.entries())
	}
	st, err := g.r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeNXDN, Target: ctl.Target{Room: "00001"}})
	if err != nil || st.Mode != ctl.ModeNXDN || st.Target == nil {
		t.Fatalf("%+v %v", st, err)
	}
	if got := h.entries(); got != "run,tune" { // the tune waited for the login
		t.Fatalf("enter: %s", got)
	}
	if n := netLogin(t, g); n.Login != ctl.LoginOK {
		t.Fatalf("in use: %+v", n)
	}
	// leaving by a mode switch: unlink, then the session ends
	if st, err = g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeLoop}); err != nil || st.Mode != ctl.ModeLoop {
		t.Fatalf("%+v %v", st, err)
	}
	if got := h.entries(); got != "run,tune,unlink,close" {
		t.Fatalf("leave: %s", got)
	}
	if n := netLogin(t, g); n.Login != ctl.LoginIdle || h.up() != 0 {
		t.Fatalf("after: %+v up %d", n, h.up())
	}
}

func TestOnDemandModeSwitchAndLeaveByTune(t *testing.T) {
	g, h := odRig(t, "ok", true, noMode, 0)
	ctx := context.Background()
	if st, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeNXDN}); err != nil || st.Mode != ctl.ModeNXDN {
		t.Fatalf("%+v %v", st, err)
	}
	if n := netLogin(t, g); n.Login != ctl.LoginOK || h.entries() != "run" {
		t.Fatalf("%+v %s", n, h.entries())
	}
	// a tune to another mode leaves it the same way
	if _, err := g.r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeLoop, Target: ctl.Target{TG: 9990, Private: true}}); err != nil {
		t.Fatal(err)
	}
	if got := h.entries(); got != "run,unlink,close" {
		t.Fatalf("%s", got)
	}
	// entering again is a new session
	if _, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeNXDN}); err != nil {
		t.Fatal(err)
	}
	if got := h.entries(); got != "run,unlink,close,run" || h.nets != 2 {
		t.Fatalf("%s nets %d", got, h.nets)
	}
}

func TestOnDemandLoginRefused(t *testing.T) {
	g, h := odRig(t, "refuse", true, noMode, 0)
	start := time.Now()
	st, err := g.r.Tune(context.Background(), ctl.TuneArgs{Mode: ctl.ModeNXDN, Target: ctl.Target{Room: "00001"}})
	if code(err) != ctl.CodeLoginFailed || !strings.Contains(err.Error(), "authentication refused") {
		t.Fatalf("%v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("a refusal waited %v", time.Since(start))
	}
	if st.Mode != ctl.ModeNone || st.Target != nil {
		t.Fatalf("%+v", st)
	}
	if got := h.entries(); got != "run,close" { // never tuned; the session is gone
		t.Fatalf("%s", got)
	}
	if n := netLogin(t, g); n.Login != ctl.LoginIdle {
		t.Fatalf("%+v", n)
	}
}

func TestOnDemandLoginTimeout(t *testing.T) {
	g, h := odRig(t, "silent", true, noMode, 300*time.Millisecond)
	_, err := g.r.Mode(context.Background(), ctl.ModeArgs{Mode: ctl.ModeNXDN})
	if code(err) != ctl.CodeLoginFailed || !strings.Contains(err.Error(), "did not answer the login") {
		t.Fatalf("%v", err)
	}
	st, _ := g.r.Status(context.Background())
	if st.Mode != ctl.ModeNone {
		t.Fatalf("%+v", st)
	}
	if got := h.entries(); got != "run,close" || h.up() != 0 {
		t.Fatalf("%s", got)
	}
	if n := netLogin(t, g); n.Login != ctl.LoginIdle {
		t.Fatalf("%+v", n)
	}
	// the master stays quiet: no session comes back by itself
	time.Sleep(200 * time.Millisecond)
	if got := h.entries(); got != "run,close" {
		t.Fatalf("%s", got)
	}
}

func TestOnDemandStayLoggedIn(t *testing.T) {
	g, h := odRig(t, "ok", false, ctl.ModeLoop, 0)
	eventually(t, 2*time.Second, func() bool { return netLogin(t, g).Login == ctl.LoginOK })
	ctx := context.Background()
	// in and out of the network: the session is never stopped
	if _, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeNXDN}); err != nil {
		t.Fatal(err)
	}
	if _, err := g.r.Mode(ctx, ctl.ModeArgs{Mode: ctl.ModeLoop}); err != nil {
		t.Fatal(err)
	}
	if got := h.entries(); got != "run,unlink" || netLogin(t, g).Login != ctl.LoginOK {
		t.Fatalf("%s", got)
	}
}

// Resume after a restart follows the resumed mode: the engine starts in no
// mode, and the resume is a tune or a mode switch like any other.
func TestOnDemandResumeIntoOtherModeDoesNotLogIn(t *testing.T) {
	g, h := odRig(t, "ok", true, noMode, 0)
	ctx := context.Background()
	if _, err := g.r.Tune(ctx, ctl.TuneArgs{Mode: ctl.ModeLoop, Target: ctl.Target{TG: 9990, Private: true}}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if h.entries() != "" || netLogin(t, g).Login != ctl.LoginIdle {
		t.Fatalf("%s", h.entries())
	}
}

// An initial mode that is on demand logs in at the start (no waiting).
func TestOnDemandInitialMode(t *testing.T) {
	g, h := odRig(t, "ok", true, ctl.ModeNXDN, 0)
	eventually(t, 2*time.Second, func() bool { return netLogin(t, g).Login == ctl.LoginOK })
	if h.entries() != "run" {
		t.Fatalf("%s", h.entries())
	}
}
