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
	DVFileP25Gateway: "P25Gateway.ini", DVFileNXDNGateway: "NXDNGateway.ini", DVFileDVSwitch: "DVSwitch.ini",
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
	o.DVSleep = func(context.Context, time.Duration) {} // never wait for a login for real
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

// noStock drops the stock [Info] warning: the fixtures hold the stock file.
func noStock(w []string) []string {
	out := []string{}
	for _, x := range w {
		if x != DVInfoStockWarning {
			out = append(out, x)
		}
	}
	return out
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
	for _, c := range []DVCredentials{{Port: ip(0)}, {Port: ip(65536)}, {Address: sp("bad host")}, {Address: sp("")}} {
		c := c
		if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &c}); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("%+v: %v", c, err)
		}
	}
	for _, m := range []string{"", "DSTAR", "dmr", "YSFW"} {
		if _, err := o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: m}); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("boot mode %q: %v", m, err)
		}
	}
	// empty requests are a harmless no-op, not an error
	for name, r := range map[string]DVSwitchRequest{"credentials": {Credentials: &DVCredentials{}}, "modes": {Modes: &DVModes{}}} {
		r := r
		var res DVApplyResult
		var err error
		switch name {
		case "credentials":
			res, err = o.DVSwitchCredentials(ctx, &r)
		default:
			res, err = o.DVSwitchModes(ctx, &r)
		}
		if err != nil || len(res.Files) != 0 || len(res.RestartHint) != 0 || res.Files == nil {
			t.Errorf("empty %s: %+v %v", name, res, err)
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

func TestDVCredentialsClearPassword(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Password: sp(dvTestPass)}}); err != nil {
		t.Fatal(err)
	}
	if v, _ := o.DVSwitchSetup(ctx); !v.DMR.PasswordSet {
		t.Fatal("password not set")
	}
	// both set is invalid and writes nothing
	_, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Password: sp(dvTestPass), ClearPassword: true}})
	if !errors.Is(err, ErrDVInvalid) {
		t.Fatalf("both set: %v", err)
	}
	if !strings.Contains(readDV(t, o, DVFileMMDVMBridge), dvTestPass) {
		t.Fatal("refused request changed the file")
	}
	// clear writes the stock value, not an empty one
	res, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{ClearPassword: true}})
	if err != nil || len(res.Files) == 0 {
		t.Fatalf("%v %+v", err, res)
	}
	got := readDV(t, o, DVFileMMDVMBridge)
	if strings.Contains(got, dvTestPass) || !strings.Contains(got, "Password="+dvStockPassword) {
		t.Errorf("password not reset to the stock value:\n%s", got)
	}
	if v, _ := o.DVSwitchSetup(ctx); v.DMR.PasswordSet {
		t.Error("passwordSet still true")
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

func TestDVBootModeFollowsLastMode(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	writeDVIni(t, o, DVFileYSFGateway, "Network", "Startup", "US-Example")
	writeDVIni(t, o, DVFileP25Gateway, "Network", "Startup", "10100")
	hist := len(o.dvHistoryNames(DVFileAnalogBridge))
	tg0, _ := dvIni(t, o, DVFileAnalogBridge).Get("AMBE_AUDIO", "txTg")
	// stock: ambeMode DMR on [DMR]'s ports. BM boots as DMR: nothing to
	// write in Analog_Bridge.ini; the gateways' Startup is blanked.
	res, err := o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: DVModeBM})
	if err != nil || len(res.RestartHint) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	for _, f := range res.Files {
		if f == DVFileAnalogBridge {
			t.Errorf("BM rewrote Analog_Bridge.ini: %v", res.Files)
		}
	}
	for _, id := range []string{DVFileYSFGateway, DVFileP25Gateway} {
		if v, _ := dvIni(t, o, id).Get("Network", "Startup"); v != "" {
			t.Errorf("%s Startup=%q", id, v)
		}
	}
	// YSF: ambeMode YSFN and the TLV ports of DVSwitch.ini [YSF] (swapped)
	res, err = o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: "YSF"})
	if err != nil || len(res.Files) != 1 || res.Files[0] != DVFileAnalogBridge || len(res.RestartHint) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	ab := dvIni(t, o, DVFileAnalogBridge)
	get := func(k string) string { v, _ := ab.Get("AMBE_AUDIO", k); return v }
	if get("ambeMode") != "YSFN" || get("txPort") != "33103" || get("rxPort") != "33100" {
		t.Fatalf("ambeMode=%s txPort=%s rxPort=%s", get("ambeMode"), get("txPort"), get("rxPort"))
	}
	// only on change: the same mode again writes nothing
	if res, err = o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: "YSF"}); err != nil || len(res.Files) != 0 {
		t.Fatalf("again: %+v %v", res, err)
	}
	// a node set to YSF wide by hand keeps it
	writeDVIni(t, o, DVFileAnalogBridge, "AMBE_AUDIO", "ambeMode", "YSFW")
	if res, err = o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: "YSF"}); err != nil || len(res.Files) != 0 {
		t.Fatalf("YSFW: %+v %v", res, err)
	}
	// back to DMR (from BM): DMR on [DMR]'s ports
	if _, err = o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: DVModeBM}); err != nil {
		t.Fatal(err)
	}
	ab = dvIni(t, o, DVFileAnalogBridge)
	if get("ambeMode") != "DMR" || get("txPort") != "31103" || get("rxPort") != "31100" {
		t.Fatalf("ambeMode=%s txPort=%s rxPort=%s", get("ambeMode"), get("txPort"), get("rxPort"))
	}
	// not a user change: no history copy, so the rollback list stays the user's
	if n := len(o.dvHistoryNames(DVFileAnalogBridge)); n != hist {
		t.Errorf("history %d -> %d", hist, n)
	}
	// txTg is never written
	if tg, _ := ab.Get("AMBE_AUDIO", "txTg"); tg != tg0 {
		t.Errorf("txTg %q -> %q", tg0, tg)
	}
	// a missing gateway is skipped, not an error
	d, _ := dvFileByID(DVFileP25Gateway)
	os.Remove(o.dvFilePath(d))
	if _, err := o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: "P25"}); err != nil {
		t.Fatal(err)
	}
	// other fields are refused
	if _, err := o.DVSwitchBootMode(ctx, &DVSwitchRequest{Mode: "P25", Modes: &DVModes{}}); err == nil {
		t.Error("extra field accepted")
	}
	// the allowlist only takes a blank Startup
	if _, err := o.dvApply(ctx, []dvChange{{DVFileYSFGateway, "Network", "Startup", "US-Example"}}, nil); !errors.Is(err, ErrDVInvalid) {
		t.Errorf("Startup value accepted: %v", err)
	}
	if _, err := o.dvApply(ctx, []dvChange{{DVFileAnalogBridge, "AMBE_AUDIO", "txTg", "91"}}, nil); err == nil {
		t.Error("txTg accepted")
	}
}

func TestDVInstallDefaultsAllOff(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	writeDVIni(t, o, DVFileMMDVMBridge, "DMR", "Enable", "1")
	writeDVIni(t, o, DVFileMMDVMBridge, "DMR Network", "Enable", "1")
	writeDVIni(t, o, DVFileYSFGateway, "Network", "Startup", "US-Example")
	if err := o.dvBMEnabledSet(true); err != nil {
		t.Fatal(err)
	}
	var sb strings.Builder
	if err := o.dvInstallDefaults(ctx, &sb); err != nil {
		t.Fatal(err)
	}
	m := readDV(t, o, DVFileMMDVMBridge)
	if strings.Contains(m, "Enable=1") || strings.Contains(m, "Enable = 1") {
		t.Errorf("a mode is still on:\n%s", m)
	}
	if o.dvBMEnabled() {
		t.Error("BrandMeister still enabled")
	}
	if v, _ := dvIni(t, o, DVFileYSFGateway).Get("Network", "Startup"); v != "" {
		t.Errorf("Startup=%q", v)
	}
	v, _ := o.DVSwitchSetup(ctx)
	if v.Modes.DMR || v.Modes.YSF || v.Modes.P25 || v.Modes.NXDN || v.BrandMeister.Enabled || v.TGIF.Enabled {
		t.Fatalf("%+v %+v", v.Modes, v.BrandMeister)
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
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "02"}}); err != nil {
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

// The startup op was removed: the helper refuses it as an unknown op, and
// the strict decoder refuses a startup request field.
func TestDVStartupOpRemoved(t *testing.T) {
	o, _ := cfgOps(t)
	if _, err := o.HandleRequest(context.Background(), Request{Op: "dvswitch-startup"}); err == nil {
		t.Fatal("dvswitch-startup still served")
	}
	var req Request
	dec := json.NewDecoder(strings.NewReader(`{"op":"dvswitch-modes","dvswitch":{"startup":{}}}`))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err == nil || !strings.Contains(err.Error(), "startup") {
		t.Fatalf("startup accepted: %v", err)
	}
}

func TestDemoDVSwitchCfg(t *testing.T) {
	d := NewDemoDVSwitch()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := d.DVSwitchModes(ctx, DVSwitchRequest{Modes: &DVModes{P25: bp(true)}}); !errors.Is(err, ErrDVNotInstalled) {
		t.Fatal(err)
	}
	d.DVSwitchInstall(ctx)
	now = now.Add(time.Minute)
	if _, err := d.DVSwitchCredentials(ctx, DVSwitchRequest{Credentials: &DVCredentials{Password: sp("bad pass")}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	if _, err := d.DVSwitchCredentials(ctx, DVSwitchRequest{Credentials: &DVCredentials{Password: sp(dvTestPass)}}); err != nil {
		t.Fatal(err)
	}
	v, _ := d.DVSwitchSetup(ctx)
	b, _ := json.Marshal(v)
	if _, err := d.DVSwitchCredentials(ctx, DVSwitchRequest{Credentials: &DVCredentials{Password: sp(dvTestPass), ClearPassword: true}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatalf("both set: %v", err)
	}
	if strings.Contains(string(b), dvTestPass) || !v.DMR.PasswordSet {
		t.Fatalf("%s", b)
	}
	if _, err := d.DVSwitchRollback(ctx, DVSwitchRequest{File: DVFileMMDVMBridge}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DVSwitchRollback(ctx, DVSwitchRequest{File: DVFileMMDVMBridge}); !errors.Is(err, ErrDVNoHistory) {
		t.Fatal(err)
	}
	if r, err := d.DVSwitchRestart(ctx, DVSwitchRequest{Scope: "gateways"}); err != nil || len(r.Units) != 3 {
		t.Fatalf("%+v %v", r, err)
	}
}

func TestDVNXDNID(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	v, _ := o.DVSwitchSetup(ctx)
	if v.Identity.NXDNID != "" {
		t.Fatalf("stock 12345 must read as not set: %q", v.Identity.NXDNID)
	}
	if w := noStock(v.Warnings); len(w) != 0 {
		t.Fatalf("NXDN disabled, no warning expected: %v", w)
	}
	// NXDN on without an ID: warning, nothing blocked
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{NXDN: bp(true)}}); err != nil {
		t.Fatal(err)
	}
	if v, _ = o.DVSwitchSetup(ctx); !v.Modes.NXDN || len(noStock(v.Warnings)) != 1 || noStock(v.Warnings)[0] != DVNXDNWarning {
		t.Fatalf("%+v %v", v.Modes, v.Warnings)
	}
	before := readDV(t, o, DVFileMMDVMBridge)
	// empty NXDN ID leaves the file value alone
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01"}}); err != nil {
		t.Fatal(err)
	}
	if after := readDV(t, o, DVFileMMDVMBridge); !strings.Contains(after, "[NXDN]\nEnable=1\nRAN=1\nId=12345\n") || lineDiff(before, after) > 1 {
		t.Fatalf("NXDN section changed:\n%s", after)
	}
	// a valid ID is written to [NXDN] Id only, with a history copy
	before = readDV(t, o, DVFileMMDVMBridge)
	if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01", NXDNID: "65519"}}); err != nil {
		t.Fatal(err)
	}
	after := readDV(t, o, DVFileMMDVMBridge)
	if !strings.Contains(after, "[NXDN]\nEnable=1\nRAN=1\nId=65519\n") || lineDiff(before, after) != 1 {
		t.Fatalf("NXDN Id not written:\n%s", after)
	}
	if !strings.Contains(after, "Id=123456701") {
		t.Error("General Id lost")
	}
	v, _ = o.DVSwitchSetup(ctx)
	if v.Identity.NXDNID != "65519" || len(noStock(v.Warnings)) != 0 {
		t.Fatalf("%+v %v", v.Identity, v.Warnings)
	}
	// invalid values: refused, nothing written, value not echoed
	snap := readDV(t, o, DVFileMMDVMBridge)
	for _, bad := range []string{"0", "00012", "012345", "65520", "99999", "100000", "-5", "+5", "1.5", "12 3", "abc", "１２３", "1234567890123456789012"} {
		_, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01", NXDNID: bad}})
		if !errors.Is(err, ErrDVInvalid) || strings.Contains(err.Error(), bad) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if readDV(t, o, DVFileMMDVMBridge) != snap {
		t.Error("file changed by a refused request")
	}
	for _, ok := range []string{"1", "12345", "65519"} {
		if !vNXDNID(ok) {
			t.Errorf("%q refused", ok)
		}
	}
}

func TestDVYSFStartupValidator(t *testing.T) {
	good := []string{"12345", "00001", "US-Example", "Example Reflector 1", "a", strings.Repeat("x", 32), "Ünï Test", "FCS00120"}
	bad := []string{"", " lead", "trail ", "a;b", "a#b", "a=b", "tab\there", "new\nline", "nul\x00", strings.Repeat("x", 33), "bad\xffutf"}
	for _, s := range good {
		if !vYSFStartup(s) {
			t.Errorf("%q refused", s)
		}
	}
	for _, s := range bad {
		if vYSFStartup(s) {
			t.Errorf("%q accepted", s)
		}
	}
	// reDVTune is unchanged: still no spaces
	if reDVTune.MatchString("a b") || !reDVTune.MatchString("US-Example") {
		t.Error("reDVTune changed")
	}
}
