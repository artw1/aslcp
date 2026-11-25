package bridge_test

import (
	"context"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/network"
)

// quietNet stands in for a reflector network (M17 here): it has no login and,
// like the real clients, announces nothing while it is not tuned, because its
// state is already "off".
type quietNet struct{}

func (quietNet) Mode() string                           { return ctl.ModeM17 }
func (quietNet) Codec() network.Codec                   { return network.CodecAMBE }
func (quietNet) StartTX(network.CallHeader) error       { return nil }
func (quietNet) WriteVoice(network.VoiceFrame) error    { return nil }
func (quietNet) EndTX(uint64) error                     { return nil }
func (quietNet) Status() ctl.NetStatus                  { return ctl.NetStatus{Mode: ctl.ModeM17, Available: true} }
func (quietNet) Tune(context.Context, ctl.Target) error { return nil }
func (quietNet) Unlink(context.Context) error           { return nil }
func (quietNet) Run(ctx context.Context, _ network.Sink) error {
	<-ctx.Done()
	return nil
}

// A network without a login reads "off" while enabled and not tuned, never "pending".
func TestReflectorNetworkNotTunedIsOff(t *testing.T) {
	network.Register(ctl.ModeM17, func(ctl.NetConfig, ctl.Identity) (network.Network, error) { return quietNet{}, nil })
	g := newRig(t, rigOpts{cfg: func(c *ctl.Config) {
		c.Networks = append(c.Networks, ctl.NetConfig{Mode: ctl.ModeM17, Enabled: true})
	}})
	time.Sleep(100 * time.Millisecond)
	st, err := g.r.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range st.Networks {
		if n.Mode == ctl.ModeM17 {
			if n.Login != ctl.LoginOff {
				t.Fatalf("M17 login = %q, want off", n.Login)
			}
			return
		}
	}
	t.Fatal("no M17 network")
}
