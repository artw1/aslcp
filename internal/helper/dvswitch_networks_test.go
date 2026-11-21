package helper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func netView(v DVSwitchSetup, id string) DVNetworkView {
	for _, n := range v.Networks {
		if n.ID == id {
			return n
		}
	}
	return DVNetworkView{}
}

func netAction(t *testing.T, o *Ops, id, action string) DVApplyResult {
	t.Helper()
	res, err := o.DVSwitchNetwork(context.Background(), &DVSwitchRequest{Network: &DVNetworkReq{ID: id, Action: action}})
	if err != nil {
		t.Fatalf("%s %s: %v", action, id, err)
	}
	return res
}

// netOn sets a network's enabled flag in the state only (the ini is not
// touched), keeping its seeded settings.
func netOn(t *testing.T, o *Ops, id string) {
	t.Helper()
	if err := o.dvNetUpdate(func(st *dvNetworksState) {
		s := st.Networks[id]
		s.Enabled = true
		st.Networks[id] = s
	}); err != nil {
		t.Fatal(err)
	}
}

// The lines of the real DMR_Hosts.txt around TGIF, with placeholder rows.
const dvNetHosts = "#                               TGIF Network Hosts Below\n" +
	"TGIF_Backup                     0000    203.0.113.5                             passw0rd        62031\n" +
	"TGIF_Network                    0000    tgif.network                            passw0rd        62031\n" +
	"BM_2001_Example                 2001    192.0.2.20                              passw0rd        62031\n" +
	"Other_Example                   0000    192.0.2.21                              passw0rd        62031\n"

func TestDVNetworkRegistry(t *testing.T) {
	seen := map[string]bool{}
	catchAll := 0
	for _, n := range DVNetworks {
		if seen[n.ID] || n.Label == "" || (n.Via != DVViaSTFU && n.Via != DVViaMMDVM) {
			t.Errorf("entry %+v", n)
		}
		seen[n.ID] = true
		if n.CatchAll {
			catchAll++
		}
		if !DVDMRFamily(n.ID) || dvBootAmbe[n.ID][0] != DVModeDMR {
			t.Errorf("%s: not a DMR mode", n.ID)
		}
	}
	if catchAll != 1 {
		t.Errorf("%d catch-all entries", catchAll)
	}
	if got := strings.Join(DVTuneModes, ","); got != "BM,TGIF,DMR,YSF,P25,NXDN" {
		t.Errorf("tune modes %s", got)
	}
	if DVNetworkLabel(DVModeDMR) != "DMR - Other" || DVNetworkLabel("YSF") != "YSF" {
		t.Error("labels")
	}
}

// Which network a master or a DMR_Hosts.txt row belongs to comes from the
// registry; a comment line never matches.
func TestDVNetworkClaimsFromRegistry(t *testing.T) {
	rows := ParseDMRHosts([]byte(dvNetHosts))
	if len(rows) != 4 {
		t.Fatalf("rows %+v", rows)
	}
	for _, r := range rows {
		if strings.HasPrefix(r.Name, "#") {
			t.Fatalf("comment parsed as a row: %+v", r)
		}
	}
	for host, want := range map[string]string{
		"tgif.network": DVModeTGIF, "TGIF.Network.": DVModeTGIF, "203.0.113.5": DVModeTGIF,
		"192.0.2.20": DVModeBM, "3102.repeater.net": DVModeBM, "2041.master.brandmeister.network": DVModeBM,
		"192.0.2.21": "", "192.0.2.99": "", "": "",
	} {
		if got := DVNetworkClaim(host, rows); got != want {
			t.Errorf("claim %q = %q, want %q", host, got, want)
		}
	}
	// the rows DMR - Other's picker offers: the ones no other entry claims
	var other []string
	for _, r := range rows {
		claimed := false
		for _, n := range DVNetworks {
			claimed = claimed || !n.CatchAll && n.ClaimsRow(r)
		}
		if !claimed {
			other = append(other, r.Name)
		}
	}
	if strings.Join(other, ",") != "Other_Example" {
		t.Errorf("picker rows %v", other)
	}
	// TGIF's master: the named row wins over a prefix row listed first
	tg, _ := DVNetworkByID(DVModeTGIF)
	if it, ok := DVNetworkHost(tg, []byte(dvNetHosts)); !ok || it.Address != "tgif.network" || it.Port != 62031 {
		t.Errorf("TGIF host %+v %v", it, ok)
	}
	if _, ok := DVNetworkHost(tg, []byte("#                               TGIF Network Hosts Below\nOther_Example 0000 192.0.2.21 passw0rd 62031\n")); ok {
		t.Error("TGIF host found in a comment")
	}
	// on a node: the file, else the public default
	o, _ := cfgOps(t)
	if a, p, src, _ := o.dvNetMaster(tg); a != DefaultDVTGIFHost || p != DefaultDVTGIFPort || src != "default" {
		t.Errorf("default master %s:%d %s", a, p, src)
	}
	os.MkdirAll(o.DVHostDir, 0o755)
	os.WriteFile(filepath.Join(o.DVHostDir, "DMR_Hosts.txt"), []byte(dvNetHosts), 0o644)
	if a, _, src, name := o.dvNetMaster(tg); a != "tgif.network" || src != "hosts" || name != "TGIF_Network" {
		t.Errorf("hosts master %s %s %s", a, src, name)
	}
	if o.dvMMDVMNetwork("203.0.113.5") != DVModeTGIF || o.dvMMDVMNetwork("192.0.2.21") != DVModeDMR || o.dvMMDVMNetwork("192.0.2.20") != DVModeDMR {
		t.Error("ini classification")
	}
}

func TestDVNetworksStatePersistAndSeed(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	// the 1.3 state and [DMR Network] seed it
	os.MkdirAll(o.dvStateDir(), 0o700)
	for _, n := range []string{dvSTFUStayFile, dvBMEnabledFile} {
		os.WriteFile(filepath.Join(o.dvStateDir(), n), nil, 0o600)
	}
	st := o.dvNetGet()
	if !st.STFUStay || !st.Networks[DVModeBM].Enabled ||
		st.Networks[DVModeDMR] != (dvNetState{Address: "hblink.dvswitch.org", Port: 62031}) || st.Networks[DVModeTGIF] != (dvNetState{}) {
		t.Fatalf("seed %+v", st)
	}
	ini0 := readDV(t, o, DVFileMMDVMBridge)
	pw := dvTestPass
	res, err := o.DVSwitchNetwork(ctx, &DVSwitchRequest{Network: &DVNetworkReq{ID: DVModeTGIF, Action: DVNetSave, Password: &pw}})
	if err != nil || strings.Join(res.Files, ",") != DVStateNetworks {
		t.Fatalf("%+v %v", res, err)
	}
	if readDV(t, o, DVFileMMDVMBridge) != ini0 {
		t.Error("saving a network [DMR Network] does not hold wrote the ini")
	}
	fi, err := os.Stat(o.dvNetworksPath())
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", fi, err)
	}
	for _, n := range []string{dvSTFUStayFile, dvBMEnabledFile} {
		if _, err := os.Stat(filepath.Join(o.dvStateDir(), n)); err == nil {
			t.Errorf("%s kept", n)
		}
	}
	st = o.dvNetGet()
	if st.Networks[DVModeTGIF].Password != dvTestPass || !st.STFUStay || !st.Networks[DVModeBM].Enabled {
		t.Fatalf("reload %+v", st)
	}
	// the view: write-only password
	v, _ := o.DVSwitchSetup(ctx)
	if b, _ := json.Marshal(v); strings.Contains(string(b), dvTestPass) {
		t.Fatal("password in the setup view")
	}
	if tv := netView(v, DVModeTGIF); !tv.PasswordSet || tv.Address != DefaultDVTGIFHost || tv.Live || !tv.Configured || tv.Enabled {
		t.Errorf("TGIF view %+v", tv)
	}
	if dv := netView(v, DVModeDMR); !dv.Live || dv.Address != "hblink.dvswitch.org" {
		t.Errorf("DMR view %+v", dv)
	}
	// saving the network [DMR Network] holds writes the ini too
	addr, port := "192.0.2.10", 40001
	if res, err := o.DVSwitchNetwork(ctx, &DVSwitchRequest{Network: &DVNetworkReq{ID: DVModeDMR, Action: DVNetSave, Address: &addr, Port: &port}}); err != nil || len(res.Files) < 2 {
		t.Fatalf("%+v %v", res, err)
	}
	m := dvIni(t, o, DVFileMMDVMBridge)
	if a, _ := m.Get("DMR Network", "Address"); a != addr {
		t.Errorf("ini address %q", a)
	}
	if p, _ := m.Get("DMR Network", "Port"); p != "40001" {
		t.Errorf("ini port %q", p)
	}
	// strict parse
	for name, raw := range map[string]string{
		"unknown field": `{"version":1,"networks":{},"x":1}`,
		"version":       `{"version":2,"networks":{}}`,
		"tgif address":  `{"version":1,"networks":{"TGIF":{"address":"192.0.2.10"}}}`,
		"bad port":      `{"version":1,"networks":{"DMR":{"port":70000}}}`,
		"bad password":  `{"version":1,"networks":{"DMR":{"password":"a b"}}}`,
	} {
		if _, err := parseDVNetworks([]byte(raw)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if st, err := parseDVNetworks([]byte(`{"version":1,"networks":{"FUTURE":{"enabled":true},"DMR":{"enabled":true}}}`)); err != nil || len(st.Networks) != 1 {
		t.Errorf("unknown network not dropped: %+v %v", st, err)
	}
}

func TestDVNetworkSaveRefusals(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	save := func(nr DVNetworkReq) error {
		nr.Action = DVNetSave
		_, err := o.DVSwitchNetwork(ctx, &DVSwitchRequest{Network: &nr})
		return err
	}
	s := func(v string) *string { return &v }
	if err := save(DVNetworkReq{ID: DVModeDMR, Address: s("tgif.network")}); !errors.Is(err, ErrDVNetMaster) {
		t.Errorf("TGIF master on DMR - Other: %v", err)
	}
	if err := save(DVNetworkReq{ID: DVModeDMR, Address: s("3102.repeater.net")}); !errors.Is(err, ErrDVBMMaster) {
		t.Errorf("BM master on DMR - Other: %v", err)
	}
	if err := save(DVNetworkReq{ID: DVModeTGIF, Address: s("192.0.2.10")}); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("address on TGIF: %v", err)
	}
	if err := save(DVNetworkReq{ID: DVModeBM, Password: s(dvTestPass)}); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("BM through the network op: %v", err)
	}
	if err := save(DVNetworkReq{ID: "NOPE"}); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("unknown id: %v", err)
	}
	if _, err := o.DVSwitchNetwork(ctx, &DVSwitchRequest{Network: &DVNetworkReq{ID: DVModeTGIF, Action: DVNetEnable, Password: s(dvTestPass)}}); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("enable with a field: %v", err)
	}
}

// MMDVM_Bridge's [DMR] and [DMR Network] Enable are on while any of its
// networks is enabled; Enable and Disable change nothing else.
func TestDVNetworkEnableFollowsAny(t *testing.T) {
	o, _ := cfgOps(t)
	flags := func() string {
		m := dvIni(t, o, DVFileMMDVMBridge)
		a, _ := m.Get("DMR", "Enable")
		b, _ := m.Get("DMR Network", "Enable")
		return a + b
	}
	addr := func() string { a, _ := dvIni(t, o, DVFileMMDVMBridge).Get("DMR Network", "Address"); return a }
	if flags() != "00" {
		t.Fatal(flags())
	}
	netAction(t, o, DVModeTGIF, DVNetEnable)
	if flags() != "11" || addr() != "hblink.dvswitch.org" {
		t.Fatalf("TGIF on: %s %s", flags(), addr())
	}
	if res := netAction(t, o, DVModeDMR, DVNetEnable); flags() != "11" || strings.Join(res.Files, ",") != DVStateNetworks {
		t.Fatalf("DMR on: %s %+v", flags(), res)
	}
	netAction(t, o, DVModeTGIF, DVNetDisable)
	if flags() != "11" {
		t.Fatalf("DMR still on: %s", flags())
	}
	netAction(t, o, DVModeDMR, DVNetDisable)
	if flags() != "00" {
		t.Fatalf("none on: %s", flags())
	}
	// BM is not an MMDVM_Bridge network
	if err := o.dvBMEnabledSet(true); err != nil || dvMMDVMAnyEnabled(o.dvNetGet()) {
		t.Fatal("BM counted")
	}
	// disable kept the settings; nothing stored: cannot enable
	if s := o.dvNetGet().Networks[DVModeDMR]; s.Address != "hblink.dvswitch.org" || s.Enabled {
		t.Fatalf("%+v", s)
	}
	if err := o.dvNetworksReset(); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchNetwork(context.Background(), &DVSwitchRequest{Network: &DVNetworkReq{ID: DVModeDMR, Action: DVNetEnable}}); !errors.Is(err, ErrDVNetNotConfigured) {
		t.Fatalf("unconfigured enable: %v", err)
	}
}

func TestDVTuneModeDisabledPerNetwork(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	tune := func(m string) error {
		_, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: m, Tune: "91"}})
		return err
	}
	for _, m := range []string{DVModeBM, DVModeTGIF, DVModeDMR} {
		if err := tune(m); !errors.Is(err, ErrDVModeDisabled) {
			t.Errorf("%s: %v", m, err)
		}
	}
	netOn(t, o, DVModeTGIF)
	if err := tune(DVModeTGIF); errors.Is(err, ErrDVModeDisabled) {
		t.Error("TGIF still disabled")
	}
	for _, m := range []string{DVModeBM, DVModeDMR} {
		if err := tune(m); !errors.Is(err, ErrDVModeDisabled) {
			t.Errorf("%s after TGIF on: %v", m, err)
		}
	}
	if err := o.dvNetSetOne(DVModeDMR, dvNetState{Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := tune(DVModeDMR); !errors.Is(err, ErrDVNetNotConfigured) {
		t.Errorf("unconfigured DMR - Other: %v", err)
	}
}

// netIni is [DMR Network] Address, Port, Password.
func netIni(t *testing.T, o *Ops) string {
	t.Helper()
	m := dvIni(t, o, DVFileMMDVMBridge)
	var out []string
	for _, k := range []string{"Address", "Port", "Password"} {
		v, _ := m.Get("DMR Network", k)
		out = append(out, v)
	}
	return strings.Join(out, " ")
}

// A switch between the MMDVM_Bridge networks rewrites [DMR Network] and
// restarts mmdvm_bridge alone; one that matches restarts nothing.
func TestDVTuneSwitchesMMDVMNetwork(t *testing.T) {
	o, _ := tuneEnv(t, "DMR", "")
	f := o.Run.(*fakeRunner)
	ctx := context.Background()
	pw, addr, port := dvTestPass, "192.0.2.10", 62031
	if _, err := o.DVSwitchNetwork(ctx, &DVSwitchRequest{Network: &DVNetworkReq{ID: DVModeDMR, Action: DVNetSave, Address: &addr, Port: &port}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchNetwork(ctx, &DVSwitchRequest{Network: &DVNetworkReq{ID: DVModeTGIF, Action: DVNetSave, Password: &pw}}); err != nil {
		t.Fatal(err)
	}
	netOn(t, o, DVModeTGIF)
	if got := netIni(t, o); got != "192.0.2.10 62031 "+dvStockPassword {
		t.Fatalf("ini %q", got)
	}
	restarts := func() int { return f.called("systemctl restart") }
	r0 := restarts()
	tune := func(m, tg string) DVTuneResult {
		t.Helper()
		res, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: m, Tune: tg}})
		if err != nil {
			t.Fatalf("%s: %+v %v", m, res, err)
		}
		return res
	}
	// DMR - Other -> TGIF
	res := tune(DVModeTGIF, "31665")
	if !res.Switched || !res.NetRestarted || res.Mode != DVModeTGIF {
		t.Fatalf("%+v", res)
	}
	if got := netIni(t, o); got != "tgif.network 62031 "+dvTestPass {
		t.Fatalf("ini %q", got)
	}
	if restarts() != r0+1 || f.called("systemctl restart mmdvm_bridge.service") != 1 {
		t.Fatalf("restarts %v", f.calls)
	}
	// no history copy: not a user change
	hist := len(o.dvHistoryNames(DVFileMMDVMBridge))
	// TGIF -> DMR - Other
	res = tune(DVModeDMR, "91")
	if !res.NetRestarted || netIni(t, o) != "192.0.2.10 62031 "+dvStockPassword || f.called("systemctl restart mmdvm_bridge.service") != 2 || restarts() != r0+2 {
		t.Fatalf("%+v %s %v", res, netIni(t, o), f.calls)
	}
	if len(o.dvHistoryNames(DVFileMMDVMBridge)) != hist {
		t.Error("the switch made a history copy")
	}
	// already there: no restart
	res = tune(DVModeDMR, "92")
	if res.NetRestarted || restarts() != r0+2 {
		t.Fatalf("restart on a match: %+v", res)
	}
	// the state is untouched by the switches
	if st := o.dvNetGet(); st.Networks[DVModeTGIF].Password != dvTestPass || st.Networks[DVModeDMR].Address != addr {
		t.Fatalf("%+v", st)
	}
}

// The resume after a restart into TGIF while [DMR Network] holds DMR -
// Other's settings: the mode-only switch rewrites it first.
func TestDVResumeIntoTGIF(t *testing.T) {
	o, _ := tuneEnv(t, "DMR", "")
	f := o.Run.(*fakeRunner)
	ctx := context.Background()
	netOn(t, o, DVModeTGIF)
	if o.dvMMDVMLive() != DVModeDMR {
		t.Fatal("ini not on DMR - Other")
	}
	res, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: DVModeTGIF, ModeOnly: true}})
	if err != nil || !res.NetRestarted {
		t.Fatalf("%+v %v", res, err)
	}
	if o.dvMMDVMLive() != DVModeTGIF || f.called("systemctl restart mmdvm_bridge.service") != 1 {
		t.Fatalf("%s %v", netIni(t, o), f.calls)
	}
	info, _ := o.DVSwitchInfo(ctx)
	if DVPanelMode(info) != DVModeTGIF {
		t.Fatalf("panel %+v", info)
	}
}
