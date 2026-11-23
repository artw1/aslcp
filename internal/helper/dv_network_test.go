package helper

import (
	"context"
	"encoding/json"
	"errors"
	dvconfig "github.com/artw1/aslcp/internal/dv/config"
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
	rep, err := dvNetDo(o, DVNetworkRequest{Mode: "BM", Address: sp("192.0.2.10"), Password: sp("example-pass"),
		Options: map[string]string{"keepalive_ms": "1000", "client_version": ""}, Enabled: bp(true)})
	if err != nil || !rep.Rendered {
		t.Fatalf("%+v %v", rep, err)
	}
	for _, v := range rep.Networks {
		if v.Mode == "BM" && (!v.PasswordSet || !v.Enabled || v.Options["keepalive_ms"] != "1000" || len(v.Options) != 1) {
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
	if bm == nil || !bm.Enabled || bm.Address != "192.0.2.10" || bm.Port != 0 || bm.Password != "example-pass" || bm.Options["keepalive_ms"] != "1000" {
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
		"port":    {Mode: "BM", Port: ip(54006)}, // BM has no port setting
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

func renderedNet(t *testing.T, o *Ops, mode string) dvctl.NetConfig {
	t.Helper()
	for _, n := range dvRendered(t, o).Networks {
		if n.Mode == mode {
			return n
		}
	}
	t.Fatalf("%s not rendered", mode)
	return dvctl.NetConfig{}
}

func TestDVNetworkTGIFAndDMR(t *testing.T) {
	o := dvCfgOps(t)
	// incomplete: no password, no identity
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "TGIF", Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("%v", err)
	}
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "TGIF", Password: sp("example-pass"), Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("no identity: %v", err)
	}
	// DMR needs an address
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "DMR", Password: sp("example-pass"), Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("DMR without address: %v", err)
	}
	// TGIF needs no address: the public master is the default
	rep, err := dvNetDo(o, DVNetworkRequest{Mode: "TGIF", Password: sp("example-pass"), Enabled: bp(true),
		Options: map[string]string{"tune_kerchunk": "0", "colour_code": ""}})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(rep); strings.Contains(string(b), "example-pass") {
		t.Fatalf("password in reply: %s", b)
	}
	n := renderedNet(t, o, "TGIF")
	if !n.Enabled || n.Password != "example-pass" || n.Address != dvctl.TGIFAddress || n.Port != dvctl.TGIFPort || n.Options["tune_kerchunk"] != "false" || len(n.Options) != 1 {
		t.Fatalf("%+v", n)
	}
	// DMR with its own master
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "DMR", Address: sp("192.0.2.10"), Port: ip(62031), Password: sp("example-pass"), Enabled: bp(true)}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "DMR"); !n.Enabled || n.Address != "192.0.2.10" || n.Port != 62031 {
		t.Fatalf("%+v", n)
	}
	for name, r := range map[string]DVNetworkRequest{
		"slot":     {Mode: "DMR", Options: map[string]string{"slot": "3"}},
		"tgifslot": {Mode: "TGIF", Options: map[string]string{"slot": "1"}},
		"tgifopts": {Mode: "TGIF", Options: map[string]string{"options": "TS2=1997;"}},
		"bool":     {Mode: "DMR", Options: map[string]string{"tune_kerchunk": "maybe"}},
		"optlen":   {Mode: "DMR", Options: map[string]string{"options": strings.Repeat("x", 201)}},
		"location": {Mode: "DMR", Options: map[string]string{"location": strings.Repeat("x", 21)}},
		"nxdnopt":  {Mode: "DMR", Options: map[string]string{"nxdn_id": "1"}},
	} {
		if _, err := dvNetDo(o, r); !errors.Is(err, ErrDVBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// clearing the identity switches them off
	rep, _ = dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{}})
	for _, v := range rep.Networks {
		if v.Enabled {
			t.Fatalf("still enabled: %+v", v)
		}
	}
	// the password is kept and never returned
	got, _ := o.DVNetwork(context.Background(), &DVRequest{})
	for _, v := range got.Networks {
		if (v.Mode == "TGIF" || v.Mode == "DMR") && !v.PasswordSet {
			t.Fatalf("%+v", v)
		}
	}
	if b, _ := json.Marshal(got); strings.Contains(string(b), "example-pass") {
		t.Fatalf("password in read: %s", b)
	}
}

func TestDVNetworkNXDN(t *testing.T) {
	o := dvCfgOps(t)
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("no identity: %v", err)
	}
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	for name, r := range map[string]DVNetworkRequest{
		"password": {Mode: "NXDN", Password: sp("example-pass")},
		"address":  {Mode: "NXDN", Address: sp("192.0.2.10")},
		"id":       {Mode: "NXDN", Options: map[string]string{"nxdn_id": "70000"}},
		"ran":      {Mode: "NXDN", Options: map[string]string{"nxdn_ran": "64"}},
		"hbopt":    {Mode: "NXDN", Options: map[string]string{"slot": "1"}},
	} {
		if _, err := dvNetDo(o, r); !errors.Is(err, ErrDVBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true), Options: map[string]string{"nxdn_id": "1999", "nxdn_ran": "1"}}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "NXDN"); !n.Enabled || n.Password != "" || n.Options["nxdn_id"] != "1999" || n.Options["nxdn_ran"] != "1" {
		t.Fatalf("%+v", n)
	}
}

func TestDVNetworkM17(t *testing.T) {
	o := dvCfgOps(t)
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "M17", Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("no identity: %v", err)
	}
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	for name, r := range map[string]DVNetworkRequest{
		"password": {Mode: "M17", Password: sp("example-pass")},
		"address":  {Mode: "M17", Address: sp("192.0.2.10")},
		"can":      {Mode: "M17", Options: map[string]string{"can": "16"}},
		"suffix":   {Mode: "M17", Options: map[string]string{"suffix": "$"}},
		"module":   {Mode: "M17", Options: map[string]string{"module": "C"}},
		"nxdn":     {Mode: "M17", Options: map[string]string{"nxdn_id": "1"}},
	} {
		if _, err := dvNetDo(o, r); !errors.Is(err, ErrDVBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// a suffix that cannot fit next to any callsign is refused
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "M17", Enabled: bp(true), Options: map[string]string{"suffix": "ABC"}}); !errors.Is(err, ErrDVBadRequest) {
		t.Fatalf("long suffix: %v", err)
	}
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "M17", Enabled: bp(true), Options: map[string]string{"can": "3", "suffix": "R"}}); err != nil {
		t.Fatal(err)
	}
	n := renderedNet(t, o, "M17")
	if !n.Enabled || n.Password != "" || n.Address != "" || n.Options["can"] != "3" || n.Options["suffix"] != "R" {
		t.Fatalf("%+v", n)
	}
	// the engine accepts what the helper renders
	cfg := dvRendered(t, o)
	if err := dvconfig.Validate(&cfg); err != nil {
		t.Fatal(err)
	}
	// a callsign that does not fit with the suffix keeps it off in the engine config
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALLXY", DMRID: 1234567}})
	if n := renderedNet(t, o, "M17"); n.Enabled {
		t.Fatalf("%+v", n)
	}
}

func TestDVNetworkUpgradeFrom14EnabledState(t *testing.T) {
	// 1.4.0: TGIF and DMR switched on with their settings, no identity yet.
	o := dvCfgOps(t)
	os.MkdirAll(o.DVStateDir, 0o700)
	old := `{"version":1,"networks":{"TGIF":{"enabled":true,"password":"example-pass"},"DMR":{"enabled":true,"address":"192.0.2.10","port":62031,"password":"example-pass"}}}`
	if err := os.WriteFile(filepath.Join(o.DVStateDir, "networks.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	// the switch is kept (shown on), but the engine does not log in until the identity exists
	rep, err := o.DVNetwork(context.Background(), &DVRequest{})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rep.Networks {
		if (v.Mode == "TGIF" || v.Mode == "DMR") && (!v.Enabled || !v.PasswordSet) {
			t.Fatalf("%+v", v)
		}
	}
	if _, err := o.DVConfig(context.Background()); err != nil {
		t.Fatal(err)
	}
	if renderedNet(t, o, "TGIF").Enabled || renderedNet(t, o, "DMR").Enabled {
		t.Fatal("an unconfigured network was started")
	}
	if _, err := dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "TGIF"); !n.Enabled || n.Password != "example-pass" {
		t.Fatalf("%+v", n)
	}
	if n := renderedNet(t, o, "DMR"); !n.Enabled || n.Address != "192.0.2.10" {
		t.Fatalf("%+v", n)
	}
}

func TestDVNetworkYSF(t *testing.T) {
	o := dvCfgOps(t)
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "YSF", Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("no identity: %v", err)
	}
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	for name, r := range map[string]DVNetworkRequest{
		"password": {Mode: "YSF", Password: sp("example-pass")},
		"port":     {Mode: "YSF", Port: ip(42000)},
		"address":  {Mode: "YSF", Address: sp("192.0.2.20")},
		"poll":     {Mode: "YSF", Options: map[string]string{"poll_ms": "10"}},
		"tick":     {Mode: "YSF", Options: map[string]string{"tick_ms": "100"}},
		"nxdnopt":  {Mode: "YSF", Options: map[string]string{"nxdn_id": "1"}},
	} {
		if _, err := dvNetDo(o, r); !errors.Is(err, ErrDVBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// enable only: the reflector is picked by Tune
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "YSF", Enabled: bp(true), Options: map[string]string{"poll_ms": "4000", "rx_timeout_ms": ""}}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "YSF"); !n.Enabled || n.Address != "" || n.Password != "" || n.Options["poll_ms"] != "4000" || len(n.Options) != 1 {
		t.Fatalf("%+v", n)
	}
}

func TestDVNetworkP25(t *testing.T) {
	o := dvCfgOps(t)
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "P25", Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("no identity: %v", err)
	}
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	for name, r := range map[string]DVNetworkRequest{
		"password": {Mode: "P25", Password: sp("example-pass")},
		"port":     {Mode: "P25", Port: ip(41000)},
		"address":  {Mode: "P25", Address: sp("192.0.2.30")},
		"poll":     {Mode: "P25", Options: map[string]string{"poll_ms": "10"}},
		"tick":     {Mode: "P25", Options: map[string]string{"tick_ms": "100"}},
		"p25id":    {Mode: "P25", Options: map[string]string{"p25_id": "1999"}},
		"nxdnopt":  {Mode: "P25", Options: map[string]string{"nxdn_id": "1"}},
	} {
		if _, err := dvNetDo(o, r); !errors.Is(err, ErrDVBadRequest) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// enable only: the reflector is picked by Tune
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "P25", Enabled: bp(true), Options: map[string]string{"poll_ms": "4000"}}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "P25"); !n.Enabled || n.Address != "" || n.Port != 0 || n.Password != "" || n.Options["poll_ms"] != "4000" || len(n.Options) != 1 {
		t.Fatalf("%+v", n)
	}
}

// A port stored for BrandMeister by an older version is dropped on load.
func TestParseDVNetworksDropsBMPort(t *testing.T) {
	st, err := parseDVNetworks([]byte(`{"version":1,"networks":{"BM":{"enabled":true,"address":"192.0.2.10","port":62031,"password":"example-pass"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if st.Networks["BM"].Port != 0 || st.Networks["BM"].Address != "192.0.2.10" {
		t.Fatalf("%+v", st.Networks["BM"])
	}
}

// YSF and P25 stored a reflector address and port, and P25 a unit ID, before
// they became enable-only: an old file still loads and the fields are dropped.
func TestParseDVNetworksDropsRetiredYSFP25Fields(t *testing.T) {
	st, err := parseDVNetworks([]byte(`{"version":1,"networks":{` +
		`"YSF":{"enabled":true,"address":"192.0.2.20","port":42001,"options":{"poll_ms":"4000"}},` +
		`"P25":{"enabled":true,"address":"reflector.example.net","port":41001,"options":{"p25_id":"1999","poll_ms":"4000"}},` +
		`"NXDN":{"enabled":true,"options":{"nxdn_id":"1234"}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"YSF", "P25"} {
		n := st.Networks[m]
		if !n.Enabled || n.Address != "" || n.Port != 0 || n.Options["poll_ms"] != "4000" || len(n.Options) != 1 {
			t.Fatalf("%s: %+v", m, n)
		}
	}
	if n := st.Networks["NXDN"]; n.Options["nxdn_id"] != "1234" {
		t.Fatalf("NXDN: %+v", n)
	}
	// other networks keep refusing what they do not take
	if _, err := parseDVNetworks([]byte(`{"version":1,"networks":{"NXDN":{"address":"192.0.2.10"}}}`)); err == nil {
		t.Fatal("NXDN address accepted")
	}
	if _, err := parseDVNetworks([]byte(`{"version":1,"networks":{"YSF":{"options":{"p25_id":"1999"}}}}`)); err == nil {
		t.Fatal("p25_id accepted on YSF")
	}
}

// The echo test (LOOP) used to have an Enable switch and a networks.json entry:
// an old file with one (on or off, with options) still loads and the entry is
// dropped; the engine config has LOOP enabled either way.
func TestParseDVNetworksDropsLoop(t *testing.T) {
	for _, loop := range []string{`{"enabled":true}`, `{"enabled":false}`, `{"enabled":true,"address":"192.0.2.1","options":{"playback_delay_ms":"10"}}`} {
		st, err := parseDVNetworks([]byte(`{"version":1,"networks":{"LOOP":` + loop + `,"BM":{"enabled":true}}}`))
		if err != nil {
			t.Fatalf("%s: %v", loop, err)
		}
		if _, ok := st.Networks["LOOP"]; ok || !st.Networks["BM"].Enabled {
			t.Fatalf("%s: %+v", loop, st.Networks)
		}
	}
	for _, off := range []bool{true, false} {
		doc := map[bool]string{true: `{"enabled":true}`, false: `{"enabled":false}`}[off]
		st, err := parseDVNetworks([]byte(`{"version":1,"networks":{"LOOP":` + doc + `}}`))
		if err != nil {
			t.Fatal(err)
		}
		cfg := renderDVConfig(34001, 32001, st, dvctl.TGIFAddress, dvctl.TGIFPort)
		if len(cfg.Networks) != 8 || cfg.Networks[0].Mode != dvctl.ModeLoop || !cfg.Networks[0].Enabled {
			t.Fatalf("%+v", cfg.Networks)
		}
	}
}

// A request crosses the helper socket as JSON: an empty Options map ("clear
// every option") must not be dropped on the way, or it reads as "keep".
func TestDVNetworkRequestJSONKeepsEmptyOptions(t *testing.T) {
	b, err := json.Marshal(DVNetworkRequest{Mode: "DMR", Options: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	var back DVNetworkRequest
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Options == nil || len(back.Options) != 0 {
		t.Fatalf("empty options lost in %s", b)
	}
	b, _ = json.Marshal(DVNetworkRequest{Mode: "DMR"})
	back = DVNetworkRequest{}
	json.Unmarshal(b, &back)
	if back.Options != nil {
		t.Fatalf("nil options became %v", back.Options)
	}
}

func TestDVNetworkClearOptions(t *testing.T) {
	o := dvCfgOps(t)
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "DMR", Address: sp("192.0.2.10"), Enabled: bp(true), Options: map[string]string{"options": "TS2=9;"}}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "DMR"); n.Options["options"] != "TS2=9;" {
		t.Fatalf("%+v", n)
	}
	// through JSON, as the helper socket does
	var r DVNetworkRequest
	if err := json.Unmarshal([]byte(`{"mode":"DMR","options":{}}`), &r); err != nil {
		t.Fatal(err)
	}
	rep, err := dvNetDo(o, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range rep.Networks {
		if v.Mode == "DMR" && (len(v.Options) != 0 || !v.Enabled) {
			t.Fatalf("%+v", v)
		}
	}
	if n := renderedNet(t, o, "DMR"); n.Options["options"] != "" {
		t.Fatalf("options kept: %+v", n)
	}
	// an empty value for one key drops it as well
	dvNetDo(o, DVNetworkRequest{Mode: "DMR", Options: map[string]string{"options": "TS2=9;", "slot": "1"}})
	rep, _ = dvNetDo(o, DVNetworkRequest{Mode: "DMR", Options: map[string]string{"options": "", "slot": "1"}})
	for _, v := range rep.Networks {
		if v.Mode == "DMR" && (v.Options["options"] != "" || v.Options["slot"] != "1") {
			t.Fatalf("%+v", v)
		}
	}
	// nil options keep everything
	rep, _ = dvNetDo(o, DVNetworkRequest{Mode: "DMR", Enabled: bp(true)})
	for _, v := range rep.Networks {
		if v.Mode == "DMR" && v.Options["slot"] != "1" {
			t.Fatalf("%+v", v)
		}
	}
}

func TestDVNetworkNXDNIDRequired(t *testing.T) {
	o := dvCfgOps(t)
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	_, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true)})
	if !errors.Is(err, ErrDVNetIncomplete) || !strings.Contains(err.Error(), "NXDN ID") {
		t.Fatalf("enable without NXDN ID: %v", err)
	}
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true), Options: map[string]string{"nxdn_id": "  "}}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("blank NXDN ID: %v", err)
	}
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true), Options: map[string]string{"nxdn_id": "12345"}}); err != nil {
		t.Fatal(err)
	}
	// clearing it while NXDN is enabled saves and turns NXDN off
	rep, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Options: map[string]string{}})
	if err != nil {
		t.Fatalf("clear while enabled: %v", err)
	}
	for _, v := range rep.Networks {
		if v.Mode == "NXDN" && (v.Enabled || v.Options["nxdn_id"] != "") {
			t.Fatalf("%+v", v)
		}
	}
	if n := renderedNet(t, o, "NXDN"); n.Enabled || n.Options["nxdn_id"] != "" {
		t.Fatalf("%+v", n)
	}
	// enabling again without an ID is refused
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true)}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("enable without ID: %v", err)
	}
	// enabling and clearing in one request is refused too
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true), Options: map[string]string{}}); !errors.Is(err, ErrDVNetIncomplete) {
		t.Fatalf("enable and clear: %v", err)
	}
	dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(true), Options: map[string]string{"nxdn_id": "12345"}})
	// switched off, it can be cleared
	if _, err := dvNetDo(o, DVNetworkRequest{Mode: "NXDN", Enabled: bp(false), Options: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "NXDN"); n.Enabled || n.Options["nxdn_id"] != "" {
		t.Fatalf("%+v", n)
	}
}

func TestDVNetworkTGIFSavedOptionsDropped(t *testing.T) {
	// an install that saved master options and a slot for TGIF: both are
	// dropped on load, DMR - Other keeps its own
	o := dvCfgOps(t)
	os.MkdirAll(o.DVStateDir, 0o700)
	old := `{"version":1,"networks":{"TGIF":{"password":"example-pass","options":{"options":"TS2=1997;","slot":"1","tune_kerchunk":"false"}},"DMR":{"address":"192.0.2.10","options":{"options":"TS2=1998;","slot":"1"}}}}`
	if err := os.WriteFile(filepath.Join(o.DVStateDir, "networks.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}}); err != nil {
		t.Fatal(err)
	}
	if n := renderedNet(t, o, "TGIF"); n.Options["options"] != "" || n.Options["slot"] != "" || n.Options["tune_kerchunk"] != "false" {
		t.Fatalf("%+v", n.Options)
	}
	raw, _ := os.ReadFile(filepath.Join(o.DVStateDir, "networks.json"))
	if strings.Contains(string(raw), "TS2=1997") {
		t.Fatalf("TGIF options still saved: %s", raw)
	}
	if n := renderedNet(t, o, "DMR"); n.Options["options"] != "TS2=1998;" || n.Options["slot"] != "1" {
		t.Fatalf("%+v", n.Options)
	}
}

func TestDVConfigTGIFMasterFollowsHostList(t *testing.T) {
	o := dvCfgOps(t)
	o.DVHostListsDir = filepath.Join(t.TempDir(), "hostlists")
	os.MkdirAll(o.DVStateDir, 0o700)
	// an install that saved an address and port for TGIF: they have no effect
	old := `{"version":1,"networks":{"TGIF":{"enabled":true,"password":"example-pass","address":"192.0.2.99","port":1234}}}`
	if err := os.WriteFile(filepath.Join(o.DVStateDir, "networks.json"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	dvNetDo(o, DVNetworkRequest{Identity: &DVIdentityView{Callsign: "N0CALL", DMRID: 1234567}})
	check := func(what, host string, port int) {
		t.Helper()
		if _, err := o.DVConfig(context.Background()); err != nil {
			t.Fatal(err)
		}
		n := renderedNet(t, o, "TGIF")
		if n.Address != host || n.Port != port || n.Password != "example-pass" {
			t.Fatalf("%s: %s:%d pw=%q", what, n.Address, n.Port, n.Password)
		}
	}
	// list missing: the built-in master
	check("missing", dvctl.TGIFAddress, dvctl.TGIFPort)
	// list without a TGIF entry
	os.MkdirAll(o.DVHostListsDir, 0o755)
	list := filepath.Join(o.DVHostListsDir, "DMR_Hosts.txt")
	os.WriteFile(list, []byte("BM_0000_Example\t\t0000\t\tbm.example.net\t\tpassw0rd\t\t62031\nDMR_Example\t\t0000\t\tdmr.example.net\t\tpassw0rd\t\t62031\n"), 0o644)
	check("no TGIF entry", dvctl.TGIFAddress, dvctl.TGIFPort)
	// list with TGIF entries: the first one, never the list's password
	os.WriteFile(list, []byte("BM_0000_Example\t\t0000\t\tbm.example.net\t\tpassw0rd\t\t62031\nTGIF_Network\t\t0000\t\ttgif1.example.net\t\tlistpass\t\t62032\nTGIF_Two\t\t0000\t\ttgif2.example.net\t\tlistpass\t\t62033\n"), 0o644)
	check("list", "tgif1.example.net", 62032)
}
