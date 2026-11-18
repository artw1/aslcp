package helper

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/dvswitch/ini"
)

// stfuOps: stock inis, identity N0CALL 1234567/01, BM password example-pass,
// MMDVM_Bridge DMR on, stfu installed, every systemctl call succeeds.
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

func dvIni(t *testing.T, o *Ops, id string) *ini.File {
	t.Helper()
	return ini.Parse([]byte(readDV(t, o, id)))
}

func toSTFU() *DVSwitchRequest {
	return &DVSwitchRequest{DMRVia: &DVDMRVia{Via: DVViaSTFU, Network: DVNetBrandMeister}}
}

func TestSTFUSwitchAndBack(t *testing.T) {
	o, f := stfuOps(t)
	ctx := context.Background()
	mmBefore, abBefore := readDV(t, o, DVFileMMDVMBridge), readDV(t, o, DVFileAnalogBridge)

	res, err := o.DVSwitchDMRVia(ctx, toSTFU())
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
	mm := dvIni(t, o, DVFileMMDVMBridge)
	for _, s := range []string{"DMR", "DMR Network"} {
		if v, _ := mm.Get(s, "Enable"); v != "0" {
			t.Errorf("[%s] Enable = %q", s, v)
		}
	}
	ab := dvIni(t, o, DVFileAnalogBridge)
	if tx, _ := ab.Get("AMBE_AUDIO", "txPort"); tx != "36103" {
		t.Errorf("AB txPort %q", tx)
	}
	if rx, _ := ab.Get("AMBE_AUDIO", "rxPort"); rx != "36100" {
		t.Errorf("AB rxPort %q", rx)
	}
	for _, c := range []string{"systemctl try-restart mmdvm_bridge.service", "systemctl enable stfu.service", "systemctl restart stfu.service"} {
		if f.called(c) != 1 {
			t.Errorf("%s: %d calls", c, f.called(c))
		}
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
	rec, err := ReadDVDMRRecord(o.dvDMRRecordPath())
	if err != nil || rec == nil || rec.Via != DVViaSTFU || !rec.PrevMMDVMDMR || !rec.ABRetargeted {
		t.Fatalf("record %+v %v", rec, err)
	}
	if fi, _ := os.Stat(o.dvDMRRecordPath()); fi.Mode().Perm() != 0o600 {
		t.Fatal("record mode", fi.Mode())
	}

	// setup view: the method, the [STFU] values, never the password
	st, err := o.DVSwitchSetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.DMRConn.Via != DVViaSTFU || st.DMRConn.Network != DVNetBrandMeister || !st.DMRConn.STFU.PasswordSet || st.DMRConn.STFU.TxPort != 36100 || !st.Modes.DMR {
		t.Fatalf("%+v", st.DMRConn)
	}
	if b, _ := json.Marshal(st); strings.Contains(string(b), "example-pass") {
		t.Fatal("password in the setup view")
	}

	// the DMR mode switch now points Analog_Bridge at [STFU]
	if s := o.dvDMRSection(); s != "STFU" {
		t.Fatal(s)
	}
	if tx, rx, err := o.dvModePorts(o.dvDMRSection()); err != nil || tx != 36100 || rx != 36103 {
		t.Fatal(tx, rx, err)
	}

	// hooks while STFU is in use
	pw := "example-pass2"
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Password: &pw}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := dvIni(t, o, DVFileDVSwitch).Get("STFU", "BMPassword"); v != pw {
		t.Fatal("BMPassword not following", v)
	}
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{ClearPassword: true}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "7654321", ESSID: "01"}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := dvIni(t, o, DVFileDVSwitch).Get("STFU", "UserID"); v != "7654321" {
		t.Fatal("UserID not following", v)
	}
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}}); err != nil {
		t.Fatal(err)
	}
	off := false
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &off}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatal("DMR off accepted with STFU", err)
	}

	// back to MMDVM_Bridge: flags and ports restored, stfu off, no record
	mmMid := readDV(t, o, DVFileMMDVMBridge)
	if _, err := o.DVSwitchDMRVia(ctx, &DVSwitchRequest{DMRVia: &DVDMRVia{Via: DVViaMMDVM}}); err != nil {
		t.Fatal(err)
	}
	if f.called("systemctl disable --now stfu.service") != 1 {
		t.Fatal("stfu not disabled")
	}
	if _, err := os.Stat(o.DVSTFUDropin); !os.IsNotExist(err) {
		t.Fatal("drop-in left after switching back", err)
	}
	if f.called("systemctl daemon-reload") != 2 {
		t.Fatal("no daemon-reload after removing the drop-in")
	}
	mm = dvIni(t, o, DVFileMMDVMBridge)
	for _, s := range []string{"DMR", "DMR Network"} {
		if v, _ := mm.Get(s, "Enable"); v != "1" {
			t.Errorf("[%s] Enable = %q after switching back", s, v)
		}
	}
	if got := readDV(t, o, DVFileAnalogBridge); got != abBefore {
		t.Error("Analog_Bridge.ini not restored")
	}
	if got := readDV(t, o, DVFileMMDVMBridge); got == mmMid || !strings.Contains(got, "Password=example-pass2") {
		t.Error("MMDVM_Bridge.ini unexpected")
	}
	_ = mmBefore
	if _, err := os.Stat(o.dvDMRRecordPath()); !os.IsNotExist(err) {
		t.Fatal("record left", err)
	}
	if s := o.dvDMRSection(); s != "DMR" {
		t.Fatal(s)
	}
}

func TestSTFUSwitchRollsBack(t *testing.T) {
	o, f := stfuOps(t)
	ctx := context.Background()
	before := map[string]string{}
	for _, id := range []string{DVFileDVSwitch, DVFileMMDVMBridge, DVFileAnalogBridge} {
		before[id] = readDV(t, o, id)
	}
	f.rules["systemctl restart stfu.service"] = resp{code: 1}
	if _, err := o.DVSwitchDMRVia(ctx, toSTFU()); !errors.Is(err, ErrDVSwitchFailed) {
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
		t.Error("drop-in left after the failed switch", err)
	}
	if _, err := os.Stat(o.dvDMRRecordPath()); !os.IsNotExist(err) {
		t.Fatal("record written", err)
	}
}

func TestSTFUSwitchRefusals(t *testing.T) {
	ctx := context.Background()
	o, f := stfuOps(t)
	bad := func(d DVDMRVia) error {
		_, err := o.DVSwitchDMRVia(ctx, &DVSwitchRequest{DMRVia: &d})
		return err
	}
	for _, d := range []DVDMRVia{
		{Via: DVViaSTFU},                            // no network
		{Via: DVViaSTFU, Network: "tgif"},           // not offered
		{Via: "hblink", Network: DVNetBrandMeister}, // unknown method
		{Via: DVViaSTFU, Network: DVNetBrandMeister, Server: "-x.example"},
		{Via: DVViaSTFU, Network: DVNetBrandMeister, Server: "a..b"},
		{Via: DVViaSTFU, Network: DVNetBrandMeister, Server: "a b"},
		{Via: DVViaMMDVM, Server: "3102.repeater.net"},
	} {
		if err := bad(d); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("%+v: %v", d, err)
		}
	}
	long := strings.Repeat("A", 28)
	if err := bad(DVDMRVia{Via: DVViaSTFU, Network: DVNetBrandMeister, TalkerAlias: &long}); !errors.Is(err, ErrDVInvalid) {
		t.Error(err)
	}
	f.rules["dpkg-query"] = resp{}
	if err := bad(*toSTFU().DMRVia); !errors.Is(err, ErrDVSTFUNotInstalled) {
		t.Error(err)
	}
	f.rules["dpkg-query"] = resp{out: "stfu\tinstalled\t1.0.0\n"}
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{ClearPassword: true}}); err != nil {
		t.Fatal(err)
	}
	if err := bad(*toSTFU().DMRVia); !errors.Is(err, ErrDVNoPassword) {
		t.Error(err)
	}
	if f.called("systemctl enable") != 0 {
		t.Error("a refused switch touched a unit")
	}
}

func TestSTFUPorts(t *testing.T) {
	ab := ini.Parse([]byte("[AMBE_AUDIO]\nambePort = 2470\n[USRP]\ntxPort = 31001\nrxPort = 31001\n"))
	cases := []struct {
		dv     string
		tx, rx int
		err    bool
	}{
		{"[DMR]\ntxPort=31100\nrxPort=31103\n[STFU]\ntxPort=36100\nrxPort=36103\n", 36100, 36103, false},
		// custom free ports are kept
		{"[DMR]\ntxPort=31100\nrxPort=31103\n[STFU]\nTXPort=36200\nrxport=36203\n", 36200, 36203, false},
		// clashing with [DMR]: back to the stock pair
		{"[DMR]\ntxPort=31100\nrxPort=31103\n[STFU]\ntxPort=31100\nrxPort=36203\n", 36100, 36103, false},
		// clashing with the USRP port, equal ports, missing keys
		{"[STFU]\ntxPort=31001\nrxPort=36203\n", 36100, 36103, false},
		{"[STFU]\ntxPort=36300\nrxPort=36300\n", 36100, 36103, false},
		{"[STFU]\n", 36100, 36103, false},
		// the stock pair taken by another section: no ports
		{"[X]\ntxPort=36100\nrxPort=40000\n[STFU]\ntxPort=31100\nrxPort=31100\n", 0, 0, true},
	}
	for i, c := range cases {
		tx, rx, err := dvSTFUPorts(ini.Parse([]byte(c.dv)), ab)
		if (err != nil) != c.err || tx != c.tx || rx != c.rx {
			t.Errorf("%d: %d %d %v", i, tx, rx, err)
		}
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

func TestSTFUDMRStatus(t *testing.T) {
	o, f := stfuOps(t)
	ctx := context.Background()
	if _, err := o.DVSwitchDMRVia(ctx, toSTFU()); err != nil {
		t.Fatal(err)
	}
	show := "systemctl show --timestamp=utc -p LoadState,ActiveState,ActiveEnterTimestamp stfu.service"
	f.rules[show] = resp{out: "LoadState=loaded\nActiveState=active\nActiveEnterTimestamp=Wed 2026-11-18 18:08:50 UTC\n"}
	if err := os.WriteFile(o.DVSTFULog, []byte(stfuLog), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := o.DVSwitchDMRStatus(ctx)
	if err != nil || st.State != DMRLoggedIn || st.Via != DVViaSTFU || st.Since != "2026-11-18T18:08:50Z" {
		t.Fatalf("%+v %v", st, err)
	}
	b, _ := json.Marshal(st)
	if strings.Contains(string(b), "example-pass") {
		t.Fatal("password in the status")
	}
	f.rules[show] = resp{out: "LoadState=loaded\nActiveState=inactive\n"}
	if st, _ := o.DVSwitchDMRStatus(ctx); st.State != DMRNotRunning {
		t.Fatal(st.State)
	}
	f.rules[show] = resp{out: "LoadState=not-found\nActiveState=inactive\n"}
	if st, _ := o.DVSwitchDMRStatus(ctx); st.State != DMRNotInstalled {
		t.Fatal(st.State)
	}
	// the restart scope "dmr" includes stfu now
	r, err := o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "dmr"})
	if err != nil || strings.Join(r.Units, ",") != "mmdvm_bridge,stfu" {
		t.Fatalf("%+v %v", r, err)
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

func TestDVDMRRecordStrict(t *testing.T) {
	p := filepath.Join(t.TempDir(), "dmr.json")
	if r, err := ReadDVDMRRecord(p); r != nil || err != nil {
		t.Fatal(r, err)
	}
	for _, s := range []string{"{", `{"version":2,"network":"brandmeister","via":"stfu"}`, `{"version":1,"network":"brandmeister","via":"stfu","x":1}`, `{"version":1,"network":"tgif","via":"stfu"}`} {
		os.WriteFile(p, []byte(s), 0o600)
		if _, err := ReadDVDMRRecord(p); !errors.Is(err, ErrDVDMRRecord) {
			t.Errorf("%s: %v", s, err)
		}
	}
	// an unreadable record shows as a setup warning and blocks the hooks
	o, _ := stfuOps(t)
	os.MkdirAll(o.dvStateDir(), 0o700)
	os.WriteFile(o.dvDMRRecordPath(), []byte("{"), 0o600)
	st, err := o.DVSwitchSetup(context.Background())
	if err != nil || !st.DMRConn.Error {
		t.Fatal(st.DMRConn, err)
	}
	found := false
	for _, w := range st.Warnings {
		found = found || w == DVDMRRecordWarning
	}
	if !found {
		t.Fatal(st.Warnings)
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

func TestDemoSTFU(t *testing.T) {
	d := NewDemoDVSwitch()
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	ctx := context.Background()
	d.installed, d.deps = true, true
	bm := &DVDMRVia{Via: DVViaSTFU, Network: DVNetBrandMeister}
	if _, err := d.DVSwitchDMRVia(ctx, DVSwitchRequest{DMRVia: bm}); !errors.Is(err, ErrDVSTFUNotInstalled) {
		t.Fatalf("not installed: %v", err)
	}
	if _, err := d.DVSwitchSTFUInstall(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DVSwitchSTFUInstall(ctx); !errors.Is(err, ErrBusy) {
		t.Fatalf("busy: %v", err)
	}
	now = now.Add(demoSTFUDuration)
	st, _ := d.DVSwitchStatus(ctx)
	if !st.STFU.Installed || st.STFU.Job.Job != JobSucceeded || st.STFU.CanInstall {
		t.Fatalf("%+v", st.STFU)
	}
	bad := &DVDMRVia{Via: DVViaSTFU, Network: DVNetBrandMeister, Server: "bad host"}
	if _, err := d.DVSwitchDMRVia(ctx, DVSwitchRequest{DMRVia: bad}); !errors.Is(err, ErrDVInvalid) {
		t.Fatalf("server: %v", err)
	}
	if _, err := d.DVSwitchDMRVia(ctx, DVSwitchRequest{DMRVia: bm}); err != nil {
		t.Fatal(err)
	}
	v, _ := d.DVSwitchSetup(ctx)
	if v.DMRConn.Via != DVViaSTFU || !v.DMRConn.STFU.Present || v.DMRConn.STFU.Server != DefaultDVSTFUServer || !v.Modes.DMR {
		t.Fatalf("%+v", v.DMRConn)
	}
	off := false
	if _, err := d.DVSwitchModes(ctx, DVSwitchRequest{Modes: &DVModes{DMR: &off}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatalf("dmr off with stfu: %v", err)
	}
	if r, _ := d.DVSwitchDMRStatus(ctx); r.Via != DVViaSTFU || r.State != DMRConnecting {
		t.Fatalf("%+v", r)
	}
	now = now.Add(demoDMRConnect)
	if r, _ := d.DVSwitchDMRStatus(ctx); r.State != DMRLoggedIn || strings.Contains(strings.ToLower(r.LastLine), "password") {
		t.Fatalf("%+v", r)
	}
	if _, err := d.DVSwitchDMRVia(ctx, DVSwitchRequest{DMRVia: &DVDMRVia{Via: DVViaMMDVM}}); err != nil {
		t.Fatal(err)
	}
	if r, _ := d.DVSwitchDMRStatus(ctx); r.Via != DVViaMMDVM {
		t.Fatalf("%+v", r)
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

func TestSTFUDropinFailureRollsBack(t *testing.T) {
	o, f := stfuOps(t)
	f.rules["systemctl daemon-reload"] = resp{code: 1}
	if _, err := o.DVSwitchDMRVia(context.Background(), toSTFU()); !errors.Is(err, ErrDVSwitchFailed) || !strings.Contains(err.Error(), "umask") {
		t.Fatal(err)
	}
	if _, err := os.Stat(o.DVSTFUDropin); !os.IsNotExist(err) {
		t.Fatal("drop-in left", err)
	}
	if f.called("systemctl enable stfu") != 0 {
		t.Fatal("stfu enabled despite the failure")
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
