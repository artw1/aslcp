package helper

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	dvconfig "github.com/artw1/aslcp/internal/dv/config"
	dvctl "github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dvpanel"
)

func TestDVAudioDefaultsRenderUnchanged(t *testing.T) {
	cfg := renderDVConfig(34001, 32001, dvEmptyNetworks(), dvctl.TGIFAddress, dvctl.TGIFPort)
	u := cfg.USRP
	if u.AGCTx || !u.AGCRx || u.GainRx != 0 || u.GainTx != 0 || u.AGCTarget != -22 || u.AGCMax != 18 {
		t.Fatalf("%+v", u)
	}
	if err := dvconfig.Validate(&cfg); err != nil {
		t.Fatal(err)
	}
}

func TestDVAudioSetAndRender(t *testing.T) {
	o := dvCfgOps(t)
	rep, err := o.DVNetwork(t.Context(), &DVRequest{})
	if err != nil || rep.Audio != dvpanel.DefaultAudio() {
		t.Fatalf("fresh: %+v, %v", rep.Audio, err)
	}
	want := dvpanel.AudioState{AGCTx: true, AGCRx: true, AGCTargetDb: -25, AGCMaxGainDb: 12, GainRxDb: 3, GainTxDb: -4}
	rep, err = dvNetDo(o, DVNetworkRequest{Audio: &want})
	if err != nil || rep.Audio != want || !rep.Rendered {
		t.Fatalf("set: %+v, %v", rep, err)
	}
	cfg := dvRendered(t, o)
	u := cfg.USRP
	if !u.AGCTx || !u.AGCRx || u.AGCTarget != -25 || u.AGCMax != 12 || u.GainRx != 3 || u.GainTx != -4 {
		t.Fatalf("rendered %+v", u)
	}
	if err := dvconfig.Validate(&cfg); err != nil {
		t.Fatal(err)
	}
	// the stored file carries it and reads back
	if rep, err = o.DVNetwork(t.Context(), &DVRequest{}); err != nil || rep.Audio != want {
		t.Fatalf("read back: %+v, %v", rep.Audio, err)
	}
	// it does not disturb the networks or the identity
	if _, err := dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}}); err != nil {
		t.Fatal(err)
	}
	if rep, _ = o.DVNetwork(t.Context(), &DVRequest{}); rep.Audio != want || rep.Identity.Callsign != "N0CALL" {
		t.Fatalf("%+v", rep)
	}
	// back to the defaults: the block leaves the file
	d := dvpanel.DefaultAudio()
	if _, err := dvNetDo(o, DVNetworkRequest{Audio: &d}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(o.DVStateDir, "networks.json"))
	if got := string(raw); strings.Contains(got, `"audio"`) {
		t.Fatalf("defaults stored: %s", got)
	}
}

func TestDVAudioInvalidAndMixed(t *testing.T) {
	o := dvCfgOps(t)
	for name, a := range map[string]dvpanel.AudioState{
		"target low":  {AGCTargetDb: -41, AGCMaxGainDb: 18},
		"target high": {AGCTargetDb: -5, AGCMaxGainDb: 18},
		"max high":    {AGCTargetDb: -22, AGCMaxGainDb: 31},
		"max low":     {AGCTargetDb: -22, AGCMaxGainDb: -1},
		"gain rx":     {AGCTargetDb: -22, AGCMaxGainDb: 18, GainRxDb: 21},
		"gain tx":     {AGCTargetDb: -22, AGCMaxGainDb: 18, GainTxDb: -21},
	} {
		a := a
		if _, err := dvNetDo(o, DVNetworkRequest{Audio: &a}); !errors.Is(err, ErrDVBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	d := dvpanel.DefaultAudio()
	if _, err := dvNetDo(o, DVNetworkRequest{Audio: &d, Identity: &DVIdentityView{}}); !errors.Is(err, ErrDVBadRequest) {
		t.Errorf("audio with identity: %v", err)
	}
}

func TestParseDVNetworksAudio(t *testing.T) {
	st, err := parseDVNetworks([]byte(`{"version":1,"audio":{"agcTx":true,"agcRx":false,"agcTargetDb":-22,"agcMaxGainDb":18,"gainRxDb":0,"gainTxDb":0},"networks":{}}`))
	if err != nil || st.Audio == nil || !st.Audio.AGCTx {
		t.Fatalf("%+v, %v", st.Audio, err)
	}
	if _, err := parseDVNetworks([]byte(`{"version":1,"audio":{"agcTargetDb":-60,"agcMaxGainDb":18},"networks":{}}`)); err == nil {
		t.Fatal("an out-of-range file was accepted")
	}
	// an all-default block is not kept; a block without agcRx means on
	st, err = parseDVNetworks([]byte(`{"version":1,"audio":{"agcTargetDb":-22,"agcMaxGainDb":18},"networks":{}}`))
	if err != nil || st.Audio != nil {
		t.Fatalf("%+v, %v", st.Audio, err)
	}
	st, err = parseDVNetworks([]byte(`{"version":1,"audio":{"agcTx":true,"agcTargetDb":-22,"agcMaxGainDb":18},"networks":{}}`))
	if err != nil || st.Audio == nil || !st.Audio.AGCRx || !st.Audio.AGCTx {
		t.Fatalf("block without agcRx: %+v, %v", st.Audio, err)
	}
	// agcRx false is not the default: the block is kept
	st, err = parseDVNetworks([]byte(`{"version":1,"audio":{"agcRx":false,"agcTargetDb":-22,"agcMaxGainDb":18},"networks":{}}`))
	if err != nil || st.Audio == nil || st.Audio.AGCRx {
		t.Fatalf("explicit off: %+v, %v", st.Audio, err)
	}
}

// Network audio AGC off is stored, rendered explicitly and read back as off.
func TestDVAudioExplicitOff(t *testing.T) {
	o := dvCfgOps(t)
	off := dvpanel.DefaultAudio()
	off.AGCRx = false
	rep, err := dvNetDo(o, DVNetworkRequest{Audio: &off})
	if err != nil || rep.Audio.AGCRx {
		t.Fatalf("set: %+v, %v", rep.Audio, err)
	}
	raw, _ := os.ReadFile(filepath.Join(o.DVStateDir, "networks.json"))
	if !regexp.MustCompile(`"agcRx":\s*false`).Match(raw) {
		t.Fatalf("off not stored: %s", raw)
	}
	cfg := dvRendered(t, o)
	if cfg.USRP.AGCRx {
		t.Fatalf("rendered on: %+v", cfg.USRP)
	}
	b, _ := json.Marshal(cfg)
	if !strings.Contains(string(b), `"agcRx":false`) {
		t.Fatalf("the engine file does not say off: %s", b)
	}
	if rep, err = o.DVNetwork(t.Context(), &DVRequest{}); err != nil || rep.Audio.AGCRx {
		t.Fatalf("read back: %+v, %v", rep.Audio, err)
	}
}
