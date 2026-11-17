package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const dvTestPass = "example-pass"

func dvFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "dvswitch", "ini", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

var dvFixtureNames = map[string]string{
	DVFileAnalogBridge: "Analog_Bridge.ini", DVFileMMDVMBridge: "MMDVM_Bridge.ini", DVFileYSFGateway: "YSFGateway.ini",
	DVFileP25Gateway: "P25Gateway.ini", DVFileNXDNGateway: "NXDNGateway.ini",
}

func dvUnitRules(units ...string) map[string]resp {
	var sb strings.Builder
	for _, u := range units {
		fmt.Fprintf(&sb, "Id=%s.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\n\n", u)
	}
	r := map[string]resp{
		"systemctl show -p Id":      {out: sb.String()},
		"systemctl show -p MainPID": {out: "MainPID=4242\n"},
		"systemctl restart":         {},
		"systemctl start":           {},
	}
	idleJob(r)
	return r
}

// cfgOps returns Ops on a temp root with all stock inis installed.
func cfgOps(t *testing.T) (*Ops, *fakeRunner) {
	t.Helper()
	f := &fakeRunner{rules: dvUnitRules(dvUnits...)}
	o := dvOps(t, f)
	root := t.TempDir()
	o.DVOptDir = filepath.Join(root, "opt")
	o.DVHistoryDir = filepath.Join(root, "hist")
	o.DVProcDir = filepath.Join(root, "proc")
	var mu sync.Mutex
	tick := 0
	o.Now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		tick++
		return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC).Add(time.Duration(tick) * time.Second)
	}
	for _, d := range dvFiles {
		p := o.dvFilePath(d)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, dvFixture(t, dvFixtureNames[d.id]), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return o, f
}

func readDV(t *testing.T, o *Ops, id string) string {
	t.Helper()
	d, _ := dvFileByID(id)
	b, err := os.ReadFile(o.dvFilePath(d))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }
func bp(b bool) *bool     { return &b }

func lineDiff(a, b string) int {
	la, lb := strings.Split(a, "\n"), strings.Split(b, "\n")
	set := map[string]int{}
	for _, l := range la {
		set[l]++
	}
	n := 0
	for _, l := range lb {
		if set[l] > 0 {
			set[l]--
		} else {
			n++
		}
	}
	return n
}

func TestDVIdentityEverywhere(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	before := readDV(t, o, DVFileMMDVMBridge)
	res, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) == 0 {
		t.Fatalf("%+v", res)
	}
	v, err := o.DVSwitchSetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if v.Identity.Callsign != "N0CALL" || v.Identity.DMRID != "1234567" || v.Identity.ESSID != "01" || !v.Identity.Consistent {
		t.Fatalf("%+v", v.Identity)
	}
	after := readDV(t, o, DVFileMMDVMBridge)
	if !strings.Contains(after, "Id=123456701") {
		t.Errorf("MMDVM Id: %s", after)
	}
	if n := lineDiff(before, after); n > 2 {
		t.Errorf("%d lines changed in MMDVM_Bridge.ini", n)
	}
	ab := readDV(t, o, DVFileAnalogBridge)
	if !strings.Contains(ab, "gatewayDmrId = 1234567") && !strings.Contains(ab, "gatewayDmrId=1234567") {
		t.Errorf("gatewayDmrId: %s", ab)
	}
	if !strings.Contains(ab, "123456701") {
		t.Error("repeaterID")
	}
	// second identical call changes nothing and offers no restart
	res, err = o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}})
	if err != nil || len(res.Files) != 0 || len(res.RestartHint) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDVValidationNeverEchoes(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	snap := map[string]string{}
	for _, d := range dvFiles {
		snap[d.id] = readDV(t, o, d.id)
	}
	bad := []DVSwitchRequest{
		{Identity: &DVIdentity{Callsign: "n0call", DMRID: "1234567", ESSID: "01"}},
		{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "12345", ESSID: "01"}},
		{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "1"}},
	}
	for _, r := range bad {
		r := r
		if _, err := o.DVSwitchIdentity(ctx, &r); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("identity %+v: %v", r.Identity, err)
		}
	}
	badPw := []string{"", "has space", `q"uote`, "a#b", "a;b", "tab\t", strings.Repeat("x", 65), "ünï"}
	for _, pw := range badPw {
		_, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Password: sp(pw)}})
		if !errors.Is(err, ErrDVInvalid) {
			t.Errorf("password %q accepted: %v", pw, err)
		} else if pw != "" && strings.Contains(err.Error(), pw) {
			t.Errorf("error echoes the password")
		}
	}
	for _, c := range []DVCredentials{{Port: ip(0)}, {Port: ip(65536)}, {Address: sp("bad host")}, {Address: sp("")}, {}} {
		c := c
		if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &c}); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	for _, s := range []DVStartup{{AmbeMode: sp("DSTAR")}, {TxTg: sp("123456789")}, {TxTs: ip(3)}, {ColorCode: ip(16)},
		{YSF: sp("a b")}, {P25: sp("abc")}, {NXDN: sp("123456")}, {}} {
		s := s
		if _, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &s}); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("%+v: %v", s, err)
		}
	}
	// a request with a field its op does not take
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: bp(true)}, Identity: &DVIdentity{}}); err == nil {
		t.Error("extra field accepted")
	}
	for _, d := range dvFiles {
		if readDV(t, o, d.id) != snap[d.id] {
			t.Errorf("%s changed by a rejected request", d.id)
		}
	}
}

func TestDVCredentialsWriteOnly(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	v, _ := o.DVSwitchSetup(ctx)
	if v.DMR.PasswordSet {
		t.Fatal("stock password counts as set")
	}
	res, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Address: sp("192.0.2.10"), Port: ip(62031), Password: sp(dvTestPass)}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(res)
	if strings.Contains(string(b), dvTestPass) {
		t.Error("apply result carries the password")
	}
	v, _ = o.DVSwitchSetup(ctx)
	b, _ = json.Marshal(v)
	if strings.Contains(string(b), dvTestPass) {
		t.Error("setup view carries the password")
	}
	if !v.DMR.PasswordSet || v.DMR.Address != "192.0.2.10" || v.DMR.Port != 62031 {
		t.Fatalf("%+v", v.DMR)
	}
	if !strings.Contains(readDV(t, o, DVFileMMDVMBridge), dvTestPass) {
		t.Error("password not written")
	}
	// the history copy is root-only
	names := o.dvHistoryNames(DVFileMMDVMBridge)
	if len(names) != 1 {
		t.Fatal(names)
	}
	fi, _ := os.Stat(filepath.Join(o.dvHistDir(DVFileMMDVMBridge), names[0]))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("history copy mode %v", fi.Mode())
	}
	di, _ := os.Stat(o.dvHistDir(DVFileMMDVMBridge))
	if di.Mode().Perm() != 0o700 {
		t.Errorf("history dir mode %v", di.Mode())
	}
}

func TestDVModesBothSections(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: bp(true), YSF: bp(true), P25: bp(false), NXDN: bp(false)}}); err != nil {
		t.Fatal(err)
	}
	v, _ := o.DVSwitchSetup(ctx)
	if !v.Modes.DMR || !v.Modes.YSF || v.Modes.P25 || v.Modes.NXDN {
		t.Fatalf("%+v", v.Modes)
	}
	// only one of the two flags set by hand: not enabled
	m := readDV(t, o, DVFileMMDVMBridge)
	if strings.Count(m, "Enable=1")+strings.Count(m, "Enable = 1") < 4 {
		t.Errorf("flags: %s", m)
	}
}

func TestDVStartupAndGatewayKeys(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	_, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{AmbeMode: sp("DMR"), TxTg: sp("91"), TxTs: ip(2), ColorCode: ip(1),
		YSF: sp("US-Example"), P25: sp("10100"), NXDN: sp("20")}})
	if err != nil {
		t.Fatal(err)
	}
	v, _ := o.DVSwitchSetup(ctx)
	if v.Startup.TxTg != "91" || v.Gateways.YSF.Startup != "US-Example" || v.Gateways.P25.Startup != "10100" || v.Gateways.NXDN.Startup != "20" {
		t.Fatalf("%+v %+v", v.Startup, v.Gateways)
	}
	// a missing gateway is skipped, not an error
	d, _ := dvFileByID(DVFileP25Gateway)
	os.Remove(o.dvFilePath(d))
	res, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{P25: sp("10200")}})
	if err != nil || len(res.Skipped) != 1 || res.Skipped[0] != DVFileP25Gateway {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDVNotInstalled(t *testing.T) {
	o, _ := cfgOps(t)
	d, _ := dvFileByID(DVFileMMDVMBridge)
	os.Remove(o.dvFilePath(d))
	_, err := o.DVSwitchIdentity(context.Background(), &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}})
	if !errors.Is(err, ErrDVNotInstalled) {
		t.Fatal(err)
	}
	// nothing was written to the other files
	if len(o.dvHistoryNames(DVFileAnalogBridge)) != 0 {
		t.Error("partial write")
	}
}

func TestDVRefusesSymlinkAndNonRegular(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	d, _ := dvFileByID(DVFileMMDVMBridge)
	p := o.dvFilePath(d)
	target := filepath.Join(t.TempDir(), "target.ini")
	os.WriteFile(target, dvFixture(t, "MMDVM_Bridge.ini"), 0o600)
	os.Remove(p)
	os.Symlink(target, p)
	req := &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}}
	if _, err := o.DVSwitchIdentity(ctx, req); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("symlink: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != string(dvFixture(t, "MMDVM_Bridge.ini")) {
		t.Error("symlink target was written")
	}
	os.Remove(p)
	os.Mkdir(p, 0o755)
	if _, err := o.DVSwitchIdentity(ctx, req); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("directory: %v", err)
	}
	v, _ := o.DVSwitchSetup(ctx)
	for _, f := range v.Files {
		if f.ID == DVFileMMDVMBridge && (f.Writable || f.SHA256 != "") {
			t.Errorf("%+v", f)
		}
	}
}

func TestDVKeepsModeAndConcurrency(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	v, _ := o.DVSwitchSetup(ctx)
	var base string
	for _, f := range v.Files {
		if f.ID == DVFileMMDVMBridge {
			base = f.SHA256
		}
	}
	req := func(b string) *DVSwitchRequest {
		return &DVSwitchRequest{Base: map[string]string{DVFileMMDVMBridge: b}, Modes: &DVModes{P25: bp(true)}}
	}
	if _, err := o.DVSwitchModes(ctx, req(base)); err != nil {
		t.Fatal(err)
	}
	d, _ := dvFileByID(DVFileMMDVMBridge)
	if fi, _ := os.Stat(o.dvFilePath(d)); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode %v", fi.Mode())
	}
	// the stale base is a conflict and nothing changes
	snap := readDV(t, o, DVFileMMDVMBridge)
	_, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Base: map[string]string{DVFileMMDVMBridge: base}, Modes: &DVModes{P25: bp(false)}})
	var ce *ConflictError
	if !errors.As(err, &ce) || len(ce.Files) != 1 || ce.Files[0] != DVFileMMDVMBridge {
		t.Fatalf("%v", err)
	}
	if readDV(t, o, DVFileMMDVMBridge) != snap {
		t.Error("changed on conflict")
	}
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Base: map[string]string{"../etc/passwd": "x"}, Modes: &DVModes{P25: bp(false)}}); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("bad base id: %v", err)
	}
}

func TestDVHistoryPruneAndRollback(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	orig := readDV(t, o, DVFileMMDVMBridge)
	if _, err := o.DVSwitchRollback(ctx, &DVSwitchRequest{File: DVFileMMDVMBridge}); !errors.Is(err, ErrDVNoHistory) {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchRollback(ctx, &DVSwitchRequest{File: "../x"}); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	for i := 0; i < 14; i++ {
		port := 40000 + i
		if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Port: &port}}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(o.dvHistoryNames(DVFileMMDVMBridge)); n != dvHistoryKeep {
		t.Fatalf("history %d", n)
	}
	// the newest copy is the state before the last write (port 40012)
	if _, err := o.DVSwitchRollback(ctx, &DVSwitchRequest{File: DVFileMMDVMBridge}); err != nil {
		t.Fatal(err)
	}
	v, _ := o.DVSwitchSetup(ctx)
	if v.DMR.Port != 40012 {
		t.Fatalf("port %d", v.DMR.Port)
	}
	if n := len(o.dvHistoryNames(DVFileMMDVMBridge)); n != dvHistoryKeep-1 {
		t.Fatalf("history %d", n)
	}
	// the first (oldest) copy was pruned: the stock file is gone for good
	if readDV(t, o, DVFileMMDVMBridge) == orig {
		t.Error("unexpected original")
	}
}

func TestDVRollbackToOriginal(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	orig := readDV(t, o, DVFileAnalogBridge)
	if _, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{TxTg: sp("4711")}}); err != nil {
		t.Fatal(err)
	}
	res, err := o.DVSwitchRollback(ctx, &DVSwitchRequest{File: DVFileAnalogBridge})
	if err != nil || len(res.RestartHint) != 1 || res.RestartHint[0] != "analog_bridge" {
		t.Fatalf("%+v %v", res, err)
	}
	if readDV(t, o, DVFileAnalogBridge) != orig {
		t.Error("not restored")
	}
}

func TestDVBusyRefused(t *testing.T) {
	o, f := cfgOps(t)
	f.mu.Lock()
	f.rules["systemctl show aslcp-dvswitch-install"] = resp{out: "ActiveState=active\nResult=success\n"}
	f.mu.Unlock()
	f.rules["systemctl list-units"] = resp{out: "aslcp-dvswitch-install.service loaded active running x\n"}
	_, err := o.DVSwitchModes(context.Background(), &DVSwitchRequest{Modes: &DVModes{P25: bp(true)}})
	if !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
}

// ---- ABInfo

func abProc(t *testing.T, o *Ops, name string, data []byte) string {
	t.Helper()
	dir := filepath.Join(o.DVProcDir, "4242", "root", "tmp")
	os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDVInfoFixture(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	if info, err := o.DVSwitchInfo(ctx); err != nil || info.Found {
		t.Fatalf("%+v %v", info, err)
	}
	data, err := os.ReadFile("testdata/ABInfo_31001.json")
	if err != nil {
		t.Fatal(err)
	}
	abProc(t, o, "ABInfo_31001.json", data)
	info, err := o.DVSwitchInfo(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Found || info.Mode != "DMR" || info.TG != "9" || info.Slot != "2" || info.ColorCode != "1" || info.GatewayID != "1234567" || info.RepeaterID != "123456789" || info.Version != "1.6.4" {
		t.Fatalf("%+v", info)
	}
}

func TestDVInfoStrictAndSafe(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	good, _ := os.ReadFile("testdata/ABInfo_31001.json")
	for name, data := range map[string][]byte{
		"unknown field": []byte(strings.Replace(string(good), `"mute"`, `"extra":"x","mute"`, 1)),
		"number":        []byte(strings.Replace(string(good), `"tg":"9"`, `"tg":9`, 1)),
		"trailing":      append(append([]byte{}, good...), []byte(`{}`)...),
		"not json":      []byte("nope"),
	} {
		os.RemoveAll(o.DVProcDir)
		abProc(t, o, "ABInfo_31001.json", data)
		if _, err := o.DVSwitchInfo(ctx); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	// over the cap
	os.RemoveAll(o.DVProcDir)
	abProc(t, o, "ABInfo_31001.json", []byte(`{"x":"`+strings.Repeat("a", dvInfoMax)+`"}`))
	if _, err := o.DVSwitchInfo(ctx); err == nil {
		t.Error("large file accepted")
	}
	// a symlink and a directory are ignored
	os.RemoveAll(o.DVProcDir)
	p := abProc(t, o, "other", good)
	os.Symlink(p, filepath.Join(filepath.Dir(p), "ABInfo_31001.json"))
	os.Mkdir(filepath.Join(filepath.Dir(p), "ABInfo_31002.json"), 0o755)
	if info, err := o.DVSwitchInfo(ctx); err != nil || info.Found {
		t.Fatalf("%+v %v", info, err)
	}
	// odd values are dropped, not echoed
	os.RemoveAll(o.DVProcDir)
	abProc(t, o, "ABInfo_31001.json", []byte(strings.Replace(string(good), `"ambe_mode":"DMR"`, `"ambe_mode":"x;rm"`, 1)))
	info, err := o.DVSwitchInfo(ctx)
	if err != nil || info.Mode != "" {
		t.Fatalf("%+v %v", info, err)
	}
	// the newest file wins
	os.RemoveAll(o.DVProcDir)
	old := abProc(t, o, "ABInfo_31001.json", good)
	os.Chtimes(old, time.Now().Add(-time.Hour), time.Now().Add(-time.Hour))
	abProc(t, o, "ABInfo_31002.json", []byte(strings.Replace(string(good), `"tg":"9"`, `"tg":"91"`, 1)))
	if info, _ := o.DVSwitchInfo(ctx); info.TG != "91" {
		t.Errorf("%+v", info)
	}
}

func TestDVStartupUseCurrent(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	if _, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}}); !errors.Is(err, ErrDVNoInfo) {
		t.Fatal(err)
	}
	good, _ := os.ReadFile("testdata/ABInfo_31001.json")
	abProc(t, o, "ABInfo_31001.json", []byte(strings.Replace(string(good), `"tg":"9"`, `"tg":"262"`, 1)))
	if _, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true, TxTg: sp("1")}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}}); err != nil {
		t.Fatal(err)
	}
	v, _ := o.DVSwitchSetup(ctx)
	if v.Startup.TxTg != "262" || v.Startup.AmbeMode != "DMR" {
		t.Fatalf("%+v", v.Startup)
	}
}

// ---- restart

func TestDVRestartScopes(t *testing.T) {
	o, f := cfgOps(t)
	ctx := context.Background()
	f.rules = dvUnitRules("analog_bridge", "mmdvm_bridge", "ysfgateway")
	res, err := o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(res.Units, ",") != "analog_bridge,mmdvm_bridge,ysfgateway" || strings.Join(res.Skipped, ",") != "p25gateway,nxdngateway" {
		t.Fatalf("%+v", res)
	}
	if f.called("systemctl restart analog_bridge.service mmdvm_bridge.service ysfgateway.service") != 1 {
		t.Fatalf("%v", f.calls)
	}
	res, err = o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "gateways", Action: "start"})
	if err != nil || res.Action != "start" || len(res.Units) != 1 || f.called("systemctl start ysfgateway.service") != 1 {
		t.Fatalf("%+v %v %v", res, err, f.calls)
	}
	if _, err := o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "x"}); !errors.Is(err, ErrDVInvalid) {
		t.Error(err)
	}
	if _, err := o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "all", Action: "stop"}); !errors.Is(err, ErrDVInvalid) {
		t.Error(err)
	}
	f.rules = dvUnitRules()
	f.rules["systemctl show -p Id"] = resp{}
	if _, err := o.DVSwitchRestart(ctx, &DVSwitchRequest{Scope: "bridge"}); !errors.Is(err, ErrDVNotInstalled) {
		t.Error(err)
	}
}

func TestDVRequestFieldOnlyForDVOps(t *testing.T) {
	o, _ := cfgOps(t)
	_, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchStatus, DVSwitch: &DVSwitchRequest{}})
	if err == nil || !strings.Contains(err.Error(), "unexpected dvswitch field") {
		t.Fatal(err)
	}
	if _, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchIdentity}); err == nil {
		t.Fatal("missing field accepted")
	}
	r, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchSetup})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.(DVSwitchSetup); !ok {
		t.Fatalf("%T", r)
	}
}
