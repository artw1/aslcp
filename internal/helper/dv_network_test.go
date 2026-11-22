package helper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	dvctl "github.com/artw1/aslcp/internal/dv/ctl"
)

func dvNetDo(o *Ops, r DVNetworkRequest) (DVNetworkReply, error) {
	return o.DVNetwork(context.Background(), &DVRequest{Network: &r})
}

func dvRendered(t *testing.T, o *Ops) dvctl.Config {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(o.DVStateDir, "config.json"))
	if err != nil {
		raw, err = os.ReadFile(filepath.Join(o.DVEngineDir, "config.json"))
	}
	if err != nil {
		t.Fatal(err)
	}
	var cfg dvctl.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	return cfg
}

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }
func bp(b bool) *bool     { return &b }

func TestDVNetworkRendersIdentityAndBM(t *testing.T) {
	o := dvCfgOps(t)
	if _, err := dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "n0call", DMRID: 1234567}}); err != nil {
		t.Fatal(err)
	}
	rep, err := dvNetDo(o, DVNetworkRequest{Mode: "BM", Address: sp("192.0.2.10"), Port: ip(62031), Password: sp("example-pass"),
		Options: map[string]string{"client_id": "4321", "slot": ""}, Enabled: bp(true)})
	if err != nil || !rep.Rendered {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, v := range rep.Networks {
		if v.Mode == "BM" && (!v.PasswordSet || !v.Enabled || v.Options["client_id"] != "4321" || len(v.Options) != 1) {
			t.Fatalf("%+v", v)
		}
	}
	if b, _ := json.Marshal(rep); strings.Contains(string(b), "example-pass") {
		t.Fatalf("password in reply: %s", b)
	}
	cfg := dvRendered(t, o)
	if cfg.Identity.Callsign != "N0CALL" || cfg.Identity.DMRID != 1234567 {
		t.Fatalf("identity %+v", cfg.Identity)
	}
	var bm *dvctl.NetConfig
	for i := range cfg.Networks {
		if cfg.Networks[i].Mode == "BM" {
			bm = &cfg.Networks[i]
		}
	}
	if bm == nil || !bm.Enabled || bm.Address != "192.0.2.10" || bm.Port != 62031 || bm.Password != "example-pass" || bm.Options["client_id"] != "4321" {
		t.Fatalf("bm %+v", bm)
	}
	// a later read never returns it either
	got, err := o.DVNetwork(context.Background(), &DVRequest{})
	if b, _ := json.Marshal(got); err != nil || strings.Contains(string(b), "example-pass") {
		t.Fatalf("%s %v", b, err)
	}
}

func TestDVNetworkIncompleteAndInvalid(t *testing.T) {
	o := dvCfgOps(t)
	// no address, password or identity yet
	_, err := dvNetDo(o, DVNetworkRequest{Mode: "BM", Enabled: bp(true)})
	if !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("%v", err)
	}
	for name, r := range map[string]DVNetworkRequest{
		"port":    {Mode: "BM", Port: ip(70000)},
		"address": {Mode: "BM", Address: sp("bad host!")},
		"option":  {Mode: "BM", Options: map[string]string{"nope": "1"}},
		"mode":    {Mode: "XYZ"},
		"call":    {Identity: &DVIdentityView{Callsign: "no good", DMRID: 1234567}},
		"dmrid":   {Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 12}},
	} {
		if _, err := dvNetDo(o, r); !errors.Is(err, ErrDVBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDVNetworkClearIdentityDisablesBM(t *testing.T) {
	o := dvCfgOps(t)
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "BM", Address: sp("192.0.2.10"), Password: sp("example-pass"), Enabled: bp(true)}); err != nil {
		t.Fatal(err)
	}
	rep, err := dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rep.Networks {
		if v.Mode == "BM" && v.Enabled {
			t.Fatalf("BM still enabled: %+v", v)
		}
	}
}

func TestDVNetworkUpgradeFrom14(t *testing.T) {
	// A 1.4.0 networks.json only knows BM's switch; it must parse and render.
	o := dvCfgOps(t)
	os.MkdirAll(o.DVStateDir, 0o700)
	old := `{"version":1,"stfuStay":true,"networks":{"BM":{"enabled":true},"TGIF":{"password":"example-pass"},"DMR":{"enabled":true,"address":"192.0.2.10","port":62031}}}`
	if err := os.WriteFile(filepath.Join(o.DVStateDir, "networks.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := o.DVNetwork(context.Background(), &DVRequest{})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]DVNetworkView{}
	for _, v := range rep.Networks {
		seen[v.Mode] = v
	}
	if !seen["BM"].Enabled || !seen["TGIF"].PasswordSet || seen["DMR"].Address != "192.0.2.10" || seen["DMR"].Port != 62031 {
		t.Fatalf("%+v", seen)
	}
	// a write keeps the 1.x flag so the file round-trips
	if _, err := dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}}); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(o.DVStateDir, "networks.json"))
	if !strings.Contains(string(raw), "stfuStay") {
		t.Fatalf("lost stfuStay: %s", raw)
	}
}
