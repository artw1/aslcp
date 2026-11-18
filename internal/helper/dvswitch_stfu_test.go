package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// stfuOps: stock inis, identity N0CALL 1234567/01, MMDVM_Bridge password
// example-pass, DMR on, stfu installed, every systemctl call succeeds. The
// fake reports every unit (stfu too) loaded and enabled.
func stfuOps(t *testing.T) (*Ops, *fakeRunner) {
	t.Helper()
	o, f := cfgOps(t)
	o.DVSTFULog = filepath.Join(t.TempDir(), "dvswitch", "STFU.log")
	o.DVSTFUDropin = filepath.Join(t.TempDir(), "stfu.service.d", "aslcp.conf")
	f.rules["dpkg-query"] = resp{out: "stfu\tinstalled\t1.0.0\n"}
	for _, k := range []string{"systemctl daemon-reload", "systemctl enable", "systemctl disable", "systemctl try-restart"} {
		f.rules[k] = resp{}
	}
	ctx := context.Background()
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}}); err != nil {
		t.Fatal(err)
	}
	pw := "example-pass"
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Password: &pw}}); err != nil {
		t.Fatal(err)
	}
	on := true
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &on}}); err != nil {
		t.Fatal(err)
	}
	return o, f
}

// stfuUnitDisabled makes the fake report stfu loaded but disabled.
func stfuUnitDisabled(f *fakeRunner) {
	var sb strings.Builder
	for _, u := range dvUnits {
		st := "enabled"
		if u == dvSTFUUnit {
			st = "disabled"
		}
		fmt.Fprintf(&sb, "Id=%s.service\nLoadState=loaded\nActiveState=active\nUnitFileState=%s\n\n", u, st)
	}
	f.mu.Lock()
	f.rules["systemctl show -p Id"] = resp{out: sb.String()}
	f.mu.Unlock()
}

func dvIni(t *testing.T, o *Ops, id string) *ini.File {
	t.Helper()
	return ini.Parse([]byte(readDV(t, o, id)))
}

// writeDVIni sets one key of a DVSwitch file directly (a hand edit).
func writeDVIni(t *testing.T, o *Ops, id, sec, key, val string) {
	t.Helper()
	f := dvIni(t, o, id)
	if _, err := f.Set(sec, key, val); err != nil {
		t.Fatal(err)
	}
	d, _ := dvFileByID(id)
	if err := os.WriteFile(o.dvFilePath(d), f.Bytes(), 0o640); err != nil {
		t.Fatal(err)
	}
}

func bmSave(pw string) *DVSwitchRequest {
	b := &DVBM{Action: DVBMSave}
	if pw != "" {
		b.Password = &pw
	}
	return &DVSwitchRequest{BM: b}
}

func TestBMSaveAndClear(t *testing.T) {
	o, f := stfuOps(t)
	ctx := context.Background()
	mmBefore, abBefore := readDV(t, o, DVFileMMDVMBridge), readDV(t, o, DVFileAnalogBridge)

	res, err := o.DVSwitchBM(ctx, bmSave("example-pass"))
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(res); strings.Contains(string(b), "example-pass") {
		t.Fatal("password in the reply")
	}
	dv := dvIni(t, o, DVFileDVSwitch)
	want := map[string]string{"BMAddress": DefaultDVSTFUServer, "BMPort": "54006", "BMPassword": "example-pass", "Address": "127.0.0.1",
		"txPort": "36100", "rxPort": "36103", "UserID": "1234567", "TalkerAlias": "N0CALL", "StartTG": "4000", "LogLevel": "3"}
	for k, v := range want {
		if got, _ := dv.Get("STFU", k); got != v {
			t.Errorf("[STFU] %s = %q, want %q", k, got, v)
		}
	}
	// DMR - Other keeps running next to it: MMDVM_Bridge and Analog_Bridge
	// are untouched
	if readDV(t, o, DVFileMMDVMBridge) != mmBefore || readDV(t, o, DVFileAnalogBridge) != abBefore {
		t.Error("MMDVM_Bridge.ini or Analog_Bridge.ini changed")
	}
	for _, c := range []string{"systemctl enable stfu.service", "systemctl restart stfu.service"} {
		if f.called(c) != 1 {
			t.Errorf("%s: %d calls", c, f.called(c))
		}
	}
	if f.called("systemctl try-restart mmdvm_bridge") != 0 {
		t.Error("MMDVM_Bridge restarted")
	}
	fi, err := os.Stat(o.DVSTFULog)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("STFU.log: %v %v", fi, err)
	}
	if b, err := os.ReadFile(o.DVSTFUDropin); err != nil || string(b) != "[Service]\nUMask=0077\n" {
		t.Fatalf("drop-in %q %v", b, err)
	}
	if f.called("systemctl daemon-reload") != 1 {
		t.Fatal("no daemon-reload after the drop-in")
	}

	// setup view: configured, both modes offered, never the password
	st, err := o.DVSwitchSetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bm := st.BrandMeister
	if !bm.Installed || !bm.Present || !bm.Enabled || !bm.Configured || !bm.PasswordSet || bm.TxPort != 36100 || bm.RxPort != 36103 || bm.Port != 54006 || !st.Modes.DMR {
		t.Fatalf("%+v", bm)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "example-pass") {
		t.Fatal("password in the setup view")
	}

	// a new DMR ID follows into [STFU] UserID
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "7654321", ESSID: "01"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := dvIni(t, o, DVFileDVSwitch).Get("STFU", "UserID"); v != "7654321" {
		t.Fatal("UserID not following", v)
	}

	// a save without a password keeps the saved one
	alias, tg := "N0CALL portable", "91"
	if _, err := o.DVSwitchBM(ctx, &DVSwitchRequest{BM: &DVBM{Action: DVBMSave, Server: "2041.master.brandmeister.network", TalkerAlias: &alias, StartTG: &tg}}); err != nil {
		t.Fatal(err)
	}
	dv = dvIni(t, o, DVFileDVSwitch)
	for k, v := range map[string]string{"BMPassword": "example-pass", "BMAddress": "2041.master.brandmeister.network", "TalkerAlias": alias, "StartTG": "91", "BMPort": "54006"} {
		if got, _ := dv.Get("STFU", k); got != v {
			t.Errorf("[STFU] %s = %q, want %q", k, got, v)
		}
	}

	// clear: stock password, unit off, drop-in gone, DMR - Other untouched
	mmBefore = readDV(t, o, DVFileMMDVMBridge)
	if _, err := o.DVSwitchBM(ctx, &DVSwitchRequest{BM: &DVBM{Action: DVBMClear}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := dvIni(t, o, DVFileDVSwitch).Get("STFU", "BMPassword"); v != dvStockPassword {
		t.Fatal("password left", v)
	}
	if f.called("systemctl disable --now stfu.service") != 1 {
		t.Fatal("stfu not disabled")
	}
	if _, err := os.Stat(o.DVSTFUDropin); !os.IsNotExist(err) {
		t.Fatal("drop-in left", err)
	}
	if st, _ := o.DVSwitchSetup(ctx); st.BrandMeister.PasswordSet || st.BrandMeister.Configured || !st.Modes.DMR {
		t.Fatalf("%+v", st.BrandMeister)
	}
	if readDV(t, o, DVFileMMDVMBridge) != mmBefore {
		t.Error("clear touched MMDVM_Bridge.ini")
	}
	// a cleared connection no longer follows the identity
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := dvIni(t, o, DVFileDVSwitch).Get("STFU", "UserID"); v != "7654321" {
		t.Fatal("UserID changed after clear", v)
	}
}

func TestBMSaveRollsBack(t *testing.T) {
	o, f := stfuOps(t)
	stfuUnitDisabled(f)
	ctx := context.Background()
	before := map[string]string{}
	for _, id := range []string{DVFileDVSwitch, DVFileMMDVMBridge, DVFileAnalogBridge} {
		before[id] = readDV(t, o, id)
	}
	f.rules["systemctl restart stfu.service"] = resp{code: 1}
	if _, err := o.DVSwitchBM(ctx, bmSave("example-pass")); !errors.Is(err, ErrDVSwitchFailed) {
		t.Fatal(err)
	}
	for id, b := range before {
		if readDV(t, o, id) != b {
			t.Errorf("%s not restored", id)
		}
	}
	if f.called("systemctl disable --now stfu.service") != 1 {
		t.Error("stfu not disabled after the failure")
	}
	if _, err := os.Stat(o.DVSTFUDropin); !os.IsNotExist(err) {
		t.Error("drop-in left after the failed save", err)
	}
}

func TestBMDropinFailureRollsBack(t *testing.T) {
	o, f := stfuOps(t)
	f.rules["systemctl daemon-reload"] = resp{code: 1}
	if _, err := o.DVSwitchBM(context.Background(), bmSave("example-pass")); !errors.Is(err, ErrDVSwitchFailed) || !strings.Contains(err.Error(), "umask") {
		t.Fatal(err)
	}
	if _, err := os.Stat(o.DVSTFUDropin); !os.IsNotExist(err) {
		t.Fatal("drop-in left", err)
	}
	if f.called("systemctl enable stfu") != 0 {
		t.Fatal("stfu enabled despite the failure")
	}
	if v, _ := dvIni(t, o, DVFileDVSwitch).Get("STFU", "BMPassword"); v != dvStockPassword {
		t.Fatal("password kept after the failure")
	}
}

func TestBMRefusals(t *testing.T) {
	ctx := context.Background()
	o, f := stfuOps(t)
	str := func(s string) *string { return &s }
	bad := func(b DVBM) error {
		_, err := o.DVSwitchBM(ctx, &DVSwitchRequest{BM: &b})
		return err
	}
	for _, b := range []DVBM{
		{Action: "move"},
		{Action: DVBMSave, Server: "-x.example", Password: str("example-pass")},
		{Action: DVBMSave, Server: "a..b", Password: str("example-pass")},
		{Action: DVBMSave, Server: "a b", Password: str("example-pass")},
		{Action: DVBMSave, Password: str("")},
		{Action: DVBMSave, Password: str(dvStockPassword)},
		{Action: DVBMSave, Password: str("example-pass"), TalkerAlias: str(strings.Repeat("A", 28))},
		{Action: DVBMSave, Password: str("example-pass"), TalkerAlias: str("a=b")},
		{Action: DVBMSave, Password: str("example-pass"), StartTG: str("x")},
		{Action: DVBMClear, Server: "3102.repeater.net"},
	} {
		if err := bad(b); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("%+v: %v", b, err)
		}
	}
	if _, err := o.DVSwitchBM(ctx, &DVSwitchRequest{BM: &DVBM{Action: DVBMClear}, Modes: &DVModes{}}); err == nil {
		t.Error("a mixed request accepted")
	}
	// no password given and none saved
	if err := bad(DVBM{Action: DVBMSave}); !errors.Is(err, ErrDVNoPassword) {
		t.Error(err)
	}
	f.rules["dpkg-query"] = resp{}
	if err := bad(*bmSave("example-pass").BM); !errors.Is(err, ErrDVSTFUNotInstalled) {
		t.Error(err)
	}
	f.rules["dpkg-query"] = resp{out: "stfu\tinstalled\t1.0.0\n"}
	// STFU's ports taken by another section
	writeDVIni(t, o, DVFileDVSwitch, "YSF", "txPort", "36100")
	if err := bad(*bmSave("example-pass").BM); !errors.Is(err, ErrDVInvalid) || !strings.Contains(err.Error(), "ports") {
		t.Error(err)
	}
	if f.called("systemctl enable") != 0 {
		t.Error("a refused save touched a unit")
	}
}

func TestDMROtherRefusesBM(t *testing.T) {
	ctx := context.Background()
	o, _ := stfuOps(t)
	os.MkdirAll(o.DVHostDir, 0o755)
	os.WriteFile(filepath.Join(o.DVHostDir, "DMR_Hosts.txt"), []byte("BM_0001_Example\t0001\t192.0.2.20\tpassw0rd\t62031\nOther\t0000\t192.0.2.21\tpassw0rd\t62031\n"), 0o644)
	for _, h := range []string{"2041.master.brandmeister.network", "3102.repeater.net", "192.0.2.20", "Example.Brandmeister.Network"} {
		h := h
		if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Address: &h}}); !errors.Is(err, ErrDVBMMaster) {
			t.Errorf("%s: %v", h, err)
		}
	}
	h := "192.0.2.21"
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Address: &h}}); err != nil {
		t.Fatal(err)
	}
	st, _ := o.DVSwitchSetup(ctx)
	if st.DMR.PointsAtBM || !st.DMR.Enabled {
		t.Fatalf("%+v", st.DMR)
	}
	// a hand-edited BM master shows a warning, nothing more
	writeDVIni(t, o, DVFileMMDVMBridge, "DMR Network", "Address", "2041.master.brandmeister.network")
	st, _ = o.DVSwitchSetup(ctx)
	found := false
	for _, w := range st.Warnings {
		found = found || w == DVDMRBMWarning
	}
	if !st.DMR.PointsAtBM || !found {
		t.Fatalf("%+v %v", st.DMR, st.Warnings)
	}
	// clearing DMR - Other turns both flags off; the warning goes
	off := false
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &off}}); err != nil {
		t.Fatal(err)
	}
	mm := dvIni(t, o, DVFileMMDVMBridge)
	for _, s := range []string{"DMR", "DMR Network"} {
		if v, _ := mm.Get(s, "Enable"); v != "0" {
			t.Errorf("[%s] Enable = %q", s, v)
		}
	}
	st, _ = o.DVSwitchSetup(ctx)
	for _, w := range st.Warnings {
		if w == DVDMRBMWarning {
			t.Fatal("warning for a cleared DMR - Other")
		}
	}
}

func TestBMDMRStatus(t *testing.T) {
	o, f := stfuOps(t)
	ctx := context.Background()
	if _, err := o.DVSwitchBM(ctx, bmSave("example-pass")); err != nil {
		t.Fatal(err)
	}
	show := "systemctl show --timestamp=utc -p LoadState,ActiveState,ActiveEnterTimestamp stfu.service"
	f.rules[show] = resp{out: "LoadState=loaded\nActiveState=active\nActiveEnterTimestamp=Wed 2026-11-18 18:08:50 UTC\n"}
	if err := os.WriteFile(o.DVSTFULog, []byte(stfuLog), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := o.DVSwitchDMRStatus(ctx)
	if err != nil || st.Via != DVViaMMDVM || st.BM == nil || st.BM.State != DMRLoggedIn || st.BM.Via != DVViaSTFU || st.BM.Since != "2026-11-18T18:08:50Z" {
		t.Fatalf("%+v %+v %v", st, st.BM, err)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "example-pass") {
		t.Fatal("password in the status")
	}
	f.rules[show] = resp{out: "LoadState=loaded\nActiveState=inactive\n"}
	if st, _ := o.DVSwitchDMRStatus(ctx); st.BM == nil || st.BM.State != DMRNotRunning {
		t.Fatalf("%+v", st.BM)
	}
	// installed but not enabled: disabled
	stfuUnitDisabled(f)
	if st, _ := o.DVSwitchDMRStatus(ctx); st.BM == nil || st.BM.State != DMRDisabled {
		t.Fatalf("%+v", st.BM)
	}
}

func TestBMRestartScope(t *testing.T) {
	o, f := stfuOps(t)
	ctx := context.Background()
	r, err := o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "dmr"})
	if err != nil || strings.Join(r.Units, ",") != "mmdvm_bridge,stfu" {
		t.Fatalf("%+v %v", r, err)
	}
	stfuUnitDisabled(f)
	r, err = o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "dmr"})
	if err != nil || strings.Join(r.Units, ",") != "mmdvm_bridge" {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestSTFUPortsFree(t *testing.T) {
	ab := ini.Parse([]byte("[AMBE_AUDIO]\nambePort = 2470\n[USRP]\ntxPort = 31001\nrxPort = 31001\n"))
	for dv, want := range map[string]bool{
		"[DMR]\ntxPort = 31100\nrxPort = 31103\n[STFU]\ntxPort = 36100\nrxPort = 36103\n": true,
		"[DMR]\ntxPort = 36100\nrxPort = 31103\n[STFU]\ntxPort = 1\n":                     false,
		"[NXDN]\ntxPort = 33100\nrxPort = 36103\n":                                        false,
	} {
		if got := dvSTFUPortsFree(ini.Parse([]byte(dv)), ab); got != want {
			t.Errorf("%q: %v", dv, got)
		}
	}
	if dvSTFUPortsFree(ini.Parse(nil), ini.Parse([]byte("[USRP]\ntxPort = 36103\n"))) {
		t.Error("USRP port clash accepted")
	}
}

func TestDVPanel(t *testing.T) {
	o, _ := stfuOps(t)
	for _, c := range []struct {
		mode   string
		tx, rx int
		want   string
	}{
		{"DMR", 36103, 36100, DVModeBM},
		{"DMR", 31103, 31100, DVModeDMR},
		{"DMR", 0, 0, DVModeDMR},
		{"YSF", 36103, 36100, "YSF"},
		{"", 0, 0, ""},
	} {
		info := DVSwitchInfo{Mode: c.mode, tlvTx: c.tx, tlvRx: c.rx}
		o.dvPanel(&info)
		if info.Panel != c.want || DVPanelMode(info) != c.want {
			t.Errorf("%+v: %q", c, info.Panel)
		}
	}
	// [STFU] on [DMR]'s ports cannot be told apart: DMR
	writeDVIni(t, o, DVFileDVSwitch, "STFU", "txPort", "31100")
	writeDVIni(t, o, DVFileDVSwitch, "STFU", "rxPort", "31103")
	info := DVSwitchInfo{Mode: "DMR", tlvTx: 31103, tlvRx: 31100}
	if o.dvPanel(&info); info.Panel != DVModeDMR {
		t.Error(info.Panel)
	}
	if DVPanelMode(DVSwitchInfo{Mode: "DMR"}) != DVModeDMR || !DVDMRFamily(DVModeBM) || DVDMRFamily("YSF") {
		t.Error("panel helpers")
	}
}

func TestDemoBM(t *testing.T) {
	d := NewDemoDVSwitch()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	ctx := context.Background()
	d.installed, d.deps = true, true
	pw := "example-pass"
	save := DVSwitchRequest{BM: &DVBM{Action: DVBMSave, Password: &pw}}
	bad := DVSwitchRequest{BM: &DVBM{Action: DVBMSave, Server: "bad host"}}
	if _, err := d.DVSwitchBM(ctx, bad); !errors.Is(err, ErrDVInvalid) {
		t.Fatalf("server: %v", err)
	}
	if _, err := d.DVSwitchBM(ctx, DVSwitchRequest{BM: &DVBM{Action: DVBMSave}}); err == nil {
		t.Fatal("save without a password accepted")
	}
	if _, err := d.DVSwitchBM(ctx, save); err != nil {
		t.Fatal(err)
	}
	v, _ := d.DVSwitchSetup(ctx)
	if !v.BrandMeister.Present || v.BrandMeister.Server != DefaultDVSTFUServer || !v.BrandMeister.PasswordSet || !v.Modes.DMR {
		t.Fatalf("%+v", v.BrandMeister)
	}
	if b, _ := json.Marshal(v); strings.Contains(string(b), pw) {
		t.Fatal("password in the demo setup view")
	}
	if r, _ := d.DVSwitchDMRStatus(ctx); r.BM == nil || r.BM.Via != DVViaSTFU || r.BM.State != DMRConnecting {
		t.Fatalf("%+v", r.BM)
	}
	now = now.Add(demoDMRConnect)
	if r, _ := d.DVSwitchDMRStatus(ctx); r.BM.State != DMRLoggedIn || strings.Contains(strings.ToLower(r.BM.LastLine), "password") {
		t.Fatalf("%+v", r.BM)
	}
	h := "2041.master.brandmeister.network"
	if _, err := d.DVSwitchCredentials(ctx, DVSwitchRequest{Credentials: &DVCredentials{Address: &h}}); !errors.Is(err, ErrDVBMMaster) {
		t.Fatalf("bm master in DMR - Other: %v", err)
	}
	if _, err := d.DVSwitchBM(ctx, DVSwitchRequest{BM: &DVBM{Action: DVBMClear}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := d.DVSwitchDMRStatus(ctx); r.BM == nil || r.BM.State != DMRDisabled {
		t.Fatalf("%+v", r.BM)
	}
}

func TestSTFUValidators(t *testing.T) {
	for _, s := range []string{"3102.repeater.net", "example.org", "192.0.2.10", "a"} {
		if !vHostname(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", "-a.example", "a-.example", ".a", "a..b", "a b", "a;b", "a.example.", strings.Repeat("a", 64) + ".example"} {
		if vHostname(s) {
			t.Errorf("%q accepted", s)
		}
	}
	for _, s := range []string{"N0CALL", "N0CALL Op", strings.Repeat("x", 27)} {
		if !vTalkerAlias(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range []string{"", " N0CALL", "N0CALL ", strings.Repeat("x", 28), "a;b", "a#b", "a=b", `a"b`, "a\tb", "aé"} {
		if vTalkerAlias(s) {
			t.Errorf("%q accepted", s)
		}
	}
}

const stfuLog = `I: 2026-11-18 18:08:53.400 DMR, Setting [STFU] BMAddress -> 3102.repeater.net
I: 2026-11-18 18:08:53.401 DMR, Setting [STFU] BMPassword -> example-pass
I: 2026-11-18 18:08:53.402 DMR, Setting [STFU] password -> example-pass Connecting to STFU server at address: 192.0.2.1:54006
I: 2026-11-18 18:08:53.486 Connecting to STFU server at address: 3102.repeater.net:54006
I0 2026-11-18 18:08:53.823 Successful connection to BM server at ip address:
I: 2026-11-18 18:09:01.000 DMR, Remote CMD: txTg=91
I: 2026-11-18 18:09:05.000 DMR, ODMR Begin Tx: src = 1234567, dst = 91 (GROUP)
I: 2026-11-18 18:09:07.000 DMR, ODMR End Tx:DMR frame count was 17 frames
I: 2026-11-18 18:09:10.000 DMR, Begin TX: src=1234567 rpt=123456701 dst=91 slot=2 cc=1 metadata=N0CALL
I: 2026-11-18 18:09:12.000 DMR, TX state = OFF, DMR frame count was 42 frames
`

func TestParseSTFULog(t *testing.T) {
	var st DVDMRStatus
	parseSTFULog([]byte(stfuLog), &st)
	if st.State != DMRLoggedIn || st.Master != "3102.repeater.net:54006" {
		t.Fatalf("%+v", st)
	}
	if st.RX == nil || st.RX.Active || st.RX.Src != "1234567" || st.RX.Dst != "91" || !st.RX.Group || st.RX.Frames != 17 || st.RX.Time != "2026-11-18 18:09:05" {
		t.Fatalf("%+v", st.RX)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "example-pass") || strings.Contains(string(b), "192.0.2.1") {
		t.Fatalf("a password line got through: %s", b)
	}

	// a reconnect after the login: connecting again
	var re DVDMRStatus
	parseSTFULog([]byte(stfuLog+"I: 2026-11-18 18:10:00.000 Connecting to STFU server at address: 3102.repeater.net:54006\n"), &re)
	if re.State != DMRConnecting {
		t.Fatal(re.State)
	}
	// an open transmission
	var rx DVDMRStatus
	parseSTFULog([]byte("I: 2026-11-18 18:09:05.000 DMR, ODMR Begin Tx: src = 1234567, dst = 1998 (PRIVATE)\n"), &rx)
	if rx.RX == nil || !rx.RX.Active || rx.RX.Group || rx.RX.Dst != "1998" {
		t.Fatalf("%+v", rx.RX)
	}
}

func TestSTFULogPrepRefusesSymlink(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	d := t.TempDir()
	target := filepath.Join(d, "target")
	os.WriteFile(target, nil, 0o644)
	o.DVSTFULog = filepath.Join(d, "STFU.log")
	if err := os.Symlink(target, o.DVSTFULog); err != nil {
		t.Skip(err)
	}
	if err := o.dvSTFULogPrep(); err == nil {
		t.Fatal("symlink accepted")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o644 {
		t.Fatal("target changed")
	}
	// an existing world-readable log becomes 0600
	os.Remove(o.DVSTFULog)
	os.WriteFile(o.DVSTFULog, []byte("x\n"), 0o644)
	if err := o.dvSTFULogPrep(); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(o.DVSTFULog); fi.Mode().Perm() != 0o600 {
		t.Fatal(fi.Mode())
	}
}

func TestSTFUInstall(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	s := o.dvSTFUInstallScript()
	for _, w := range []string{"apt-get -s -q install --no-install-recommends stfu", "dvswitch-step stfu-plan", "apt-get install -y -q", "systemctl disable --now stfu.service || true"} {
		if !strings.Contains(s, w) {
			t.Errorf("script lacks %q", w)
		}
	}
	// status: offered when DVSwitch is installed and stfu is not
	r := dvRules("dvswitch-server")
	f := &fakeRunner{rules: r}
	o = dvOps(t, f)
	st, err := o.DVSwitchStatus(context.Background())
	if err != nil || st.STFU.Installed || !st.STFU.CanInstall {
		t.Fatalf("%+v %v", st.STFU, err)
	}
	r["systemd-run"] = resp{}
	r["dpkg-query"] = resp{out: "dvswitch-server\tinstalled\t1\nstfu\tinstalled\t1.0.0\n"}
	if _, err := o.DVSwitchSTFUInstall(context.Background()); !errors.Is(err, ErrDVSTFUInstalled) {
		t.Fatal(err)
	}
	r["dpkg-query"] = resp{}
	if _, err := o.DVSwitchSTFUInstall(context.Background()); !errors.Is(err, ErrDVNotInstalled) {
		t.Fatal(err)
	}
	if f.called("systemd-run") != 0 {
		t.Fatal("refused install ran systemd-run")
	}
	r["dpkg-query"] = resp{out: "dvswitch-server\tinstalled\t1\n"}
	if _, err := o.DVSwitchSTFUInstall(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.called("systemd-run --no-block --unit=aslcp-dvswitch-stfu") != 1 {
		t.Fatal(f.calls)
	}
}

func TestSTFUPlanStep(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	os.MkdirAll(o.dvStateDir(), 0o700)
	os.WriteFile(o.dvSTFUSimPath(), []byte("Inst stfu (1.0.0 DVSwitch:stable [amd64])\n"), 0o600)
	if err := WriteDVMarker(o.dvMarkerPath(), DVSwitchMarker{Version: 1, Packages: []string{"dvswitch-base"}}); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if err := o.RunDVSwitchStep(context.Background(), StepSTFUPlan, &b); err != nil {
		t.Fatal(err, b.String())
	}
	m, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil || strings.Join(m.Packages, ",") != "dvswitch-base,stfu" {
		t.Fatal(m, err)
	}
	os.WriteFile(o.dvSTFUSimPath(), []byte("Inst stfu (1)\nInst apache2 (2)\n"), 0o600)
	if err := o.RunDVSwitchStep(context.Background(), StepSTFUPlan, &b); err == nil {
		t.Fatal("unexpected deps accepted")
	}
}

func TestSTFULogPrepRotated(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	d := t.TempDir()
	o.DVSTFULog = filepath.Join(d, "STFU.log")
	target := filepath.Join(d, "other")
	os.WriteFile(target, nil, 0o644)
	for _, n := range []string{"STFU.log", "STFU.log.1", "STFU.log.2.gz"} {
		os.WriteFile(filepath.Join(d, n), []byte("x\n"), 0o644)
	}
	os.Mkdir(filepath.Join(d, "STFU.log.3"), 0o755)
	if err := os.Symlink(target, filepath.Join(d, "STFU.log.4")); err != nil {
		t.Skip(err)
	}
	if err := o.dvSTFULogPrep(); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"STFU.log", "STFU.log.1", "STFU.log.2.gz"} {
		if fi, _ := os.Stat(filepath.Join(d, n)); fi.Mode().Perm() != 0o600 {
			t.Errorf("%s: %v", n, fi.Mode())
		}
	}
	if fi, _ := os.Stat(filepath.Join(d, "STFU.log.3")); fi.Mode().Perm() != 0o755 {
		t.Error("directory changed")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o644 {
		t.Error("symlink target changed")
	}
}

func TestSTFUDropinInUninstall(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	o.DVSTFUDropin = ""
	s := o.dvUninstallScript()
	if !strings.Contains(s, "rm -f -- "+shQuote(DefaultDVSTFUDropin)) || !strings.Contains(s, "systemctl daemon-reload") {
		t.Fatal("uninstall script does not remove the drop-in")
	}
}

// Lines as STFU 0.3.4 writes them (placeholder IDs and hosts).
func TestParseSTFULogRealShapes(t *testing.T) {
	const log = `I: 2026-11-18 18:08:53.100 DMR, Setting [STFU] UserID -> 1234567
I: 2026-11-18 18:08:53.486 Connecting to STFU server at address: 3102.repeater.net:54006
I: 2026-11-18 18:08:53.823 Successful connection to BM server at ip address: 192.0.2.1
I: 2026-11-18 18:09:01.000 DMR, Remote CMD: txTg=91
I: 2026-11-18 18:09:05.000 DMR, ODMR Begin Tx: src = 1234567, dst = 91 (GROUP)
I: 2026-11-18 18:09:07.000 DMR, ODMR End Tx:DMR frame count was 17 frames
I: 2026-11-18 18:09:10.000 DMR, Begin TX: src=1234567 rpt=123456701 dst=91 slot=2 cc=1 metadata=CALL
I: 2026-11-18 18:09:12.000 DMR, TX state = OFF, DMR frame count was 42 frames
`
	var st DVDMRStatus
	parseSTFULog([]byte(log), &st)
	if st.State != DMRLoggedIn || st.Master != "3102.repeater.net:54006" {
		t.Fatalf("%+v", st)
	}
	// the local TX lines must not touch the received-call record
	if st.RX == nil || st.RX.Active || st.RX.Src != "1234567" || st.RX.Dst != "91" || !st.RX.Group || st.RX.Frames != 17 {
		t.Fatalf("%+v", st.RX)
	}
}
