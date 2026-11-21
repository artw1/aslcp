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

// The TGIF lines of a stock DMR_Hosts.txt: a comment that names TGIF and the
// TGIF_Network row (passw0rd is the file's own placeholder).
const dmrHostsTGIF = "#                               TGIF Network Hosts Below\n" +
	"TGIF_Network                    0000    tgif.network                            passw0rd        62031\n"

func writeDMRHosts(t *testing.T, o *Ops, data string) {
	t.Helper()
	if err := os.MkdirAll(o.DVHostDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.DVHostDir, "DMR_Hosts.txt"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDVTGIFHost(t *testing.T) {
	for _, c := range []struct {
		name, data string
		ok         bool
		want       string
	}{
		{"stock", dmrHostsTGIF, true, "TGIF_Network tgif.network:62031"},
		// the exact name wins over an earlier prefix row
		{"exact first", "TGIF_Example 0000 203.0.113.30 passw0rd 62031\n" + dmrHostsTGIF, true, "TGIF_Network tgif.network:62031"},
		{"prefix, any case", "Other 0000 192.0.2.21 passw0rd 62031\ntgif_example 0000 203.0.113.30 passw0rd 62032\n", true, "tgif_example 203.0.113.30:62032"},
		// comment lines never match, not even a commented-out row
		{"comments only", "# TGIF Network Hosts Below\n# TGIF_Network 0000 192.0.2.99 passw0rd 62031\n", false, ""},
		{"none", "BM_0001_Example 0001 192.0.2.20 passw0rd 62031\n", false, ""},
	} {
		it, ok := DVTGIFHost([]byte(c.data))
		got := ""
		if ok {
			got = it.Name + " " + it.Address + ":" + itoa(it.Port)
		}
		if ok != c.ok || got != c.want {
			t.Errorf("%s: %v %q", c.name, ok, got)
		}
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestDVTGIFViewSource(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	v, _ := o.DVSwitchSetup(ctx)
	if v.TGIF.Address != DefaultDVTGIFHost || v.TGIF.Port != DefaultDVTGIFPort || v.TGIF.Source != "default" || v.TGIF.PasswordSet || v.TGIF.Enabled {
		t.Fatalf("no list: %+v", v.TGIF)
	}
	writeDMRHosts(t, o, "TGIF_Example 0000 203.0.113.30 passw0rd 62032\n")
	if v, _ = o.DVSwitchSetup(ctx); v.TGIF.Address != "203.0.113.30" || v.TGIF.Port != 62032 || v.TGIF.Source != "hosts" || v.TGIF.Name != "TGIF_Example" {
		t.Fatalf("prefix row: %+v", v.TGIF)
	}
	writeDMRHosts(t, o, dmrHostsTGIF)
	if v, _ = o.DVSwitchSetup(ctx); v.TGIF.Address != "tgif.network" || v.TGIF.Port != 62031 || v.TGIF.Source != "hosts" || v.TGIF.Name != "TGIF_Network" {
		t.Fatalf("stock: %+v", v.TGIF)
	}
}

// TGIF and DMR - Other share [DMR Network]: one is on at a time, and a
// switch keeps both profiles.
func TestDVDMRProfilesExclusive(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	writeDMRHosts(t, o, dmrHostsTGIF)
	str := func(s string) *string { return &s }
	on, off := true, false
	addr, port := "192.0.2.21", 62031
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Address: &addr, Port: &port, Password: str("example-pass")}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &on, DMRProfile: DVProfileOther}}); err != nil {
		t.Fatal(err)
	}
	// TGIF's password while Other is on: stored, the ini untouched
	mm := readDV(t, o, DVFileMMDVMBridge)
	res, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Password: str("example-pass2"), Profile: DVProfileTGIF}})
	if err != nil || len(res.Files) != 1 || res.Files[0] != DVStateDMRProfiles || len(res.RestartHint) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if readDV(t, o, DVFileMMDVMBridge) != mm {
		t.Fatal("TGIF password written to the ini while Other is on")
	}
	if fi, err := os.Stat(o.dvDMRProfilesPath()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("profile file: %v %v", fi, err)
	}
	// TGIF's master is not settable
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Address: &addr, Profile: DVProfileTGIF}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatalf("TGIF address: %v", err)
	}
	v, _ := o.DVSwitchSetup(ctx)
	if !v.DMR.Enabled || v.TGIF.Enabled || !v.TGIF.PasswordSet || v.DMR.Profile != DVProfileOther {
		t.Fatalf("%+v %+v", v.DMR, v.TGIF)
	}

	// enable TGIF: its master and password in [DMR Network], Other off
	res, err = o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &on, DMRProfile: DVProfileTGIF}})
	if err != nil || !res.DMRProfileChanged || strings.Join(res.RestartHint, ",") != "mmdvm_bridge" {
		t.Fatalf("%+v %v", res, err)
	}
	net := func() (string, string, string) {
		f := dvIni(t, o, DVFileMMDVMBridge)
		a, _ := f.Get("DMR Network", "Address")
		p, _ := f.Get("DMR Network", "Port")
		pw, _ := f.Get("DMR Network", "Password")
		return a, p, pw
	}
	if a, p, pw := net(); a != "tgif.network" || p != "62031" || pw != "example-pass2" {
		t.Fatalf("TGIF live: %s %s %s", a, p, pw)
	}
	v, _ = o.DVSwitchSetup(ctx)
	if v.DMR.Enabled || !v.TGIF.Enabled || !v.TGIF.PasswordSet || v.DMR.Address != addr || v.DMR.Port != port || !v.DMR.PasswordSet || v.DMR.Profile != DVProfileTGIF || !v.Modes.DMR {
		t.Fatalf("%+v %+v", v.DMR, v.TGIF)
	}
	b, _ := json.Marshal(v)
	if strings.Contains(string(b), "example-pass") {
		t.Fatal("a password in the setup view")
	}
	// Other's address while TGIF is on: stored for later
	addr2 := "192.0.2.22"
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{Address: &addr2}}); err != nil {
		t.Fatal(err)
	}
	if a, _, _ := net(); a != "tgif.network" {
		t.Fatal("Other's address written while TGIF is on")
	}
	// disabling Other while TGIF is on changes nothing
	mm = readDV(t, o, DVFileMMDVMBridge)
	if res, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &off, DMRProfile: DVProfileOther}}); err != nil || len(res.Files) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if readDV(t, o, DVFileMMDVMBridge) != mm {
		t.Fatal("Other's disable touched TGIF")
	}

	// back to Other: its values return, TGIF's password stays stored
	res, err = o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &on, DMRProfile: DVProfileOther}})
	if err != nil || !res.DMRProfileChanged {
		t.Fatalf("%+v %v", res, err)
	}
	if a, p, pw := net(); a != addr2 || p != "62031" || pw != "example-pass" {
		t.Fatalf("Other live: %s %s %s", a, p, pw)
	}
	v, _ = o.DVSwitchSetup(ctx)
	if !v.DMR.Enabled || v.TGIF.Enabled || !v.TGIF.PasswordSet {
		t.Fatalf("%+v %+v", v.DMR, v.TGIF)
	}
	// TGIF on and off again: Disable keeps the password and the profile
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &on, DMRProfile: DVProfileTGIF}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &off, DMRProfile: DVProfileTGIF}}); err != nil {
		t.Fatal(err)
	}
	v, _ = o.DVSwitchSetup(ctx)
	if v.Modes.DMR || v.TGIF.Enabled || v.DMR.Enabled || !v.TGIF.PasswordSet || v.DMR.Profile != DVProfileTGIF {
		t.Fatalf("%+v %+v %+v", v.Modes, v.DMR, v.TGIF)
	}
	if _, _, pw := net(); pw != "example-pass2" {
		t.Fatal("disable dropped TGIF's password")
	}
	// clearing TGIF's password while it is live writes the stock one
	if _, err := o.DVSwitchCredentials(ctx, &DVSwitchRequest{Credentials: &DVCredentials{ClearPassword: true, Profile: DVProfileTGIF}}); err != nil {
		t.Fatal(err)
	}
	if v, _ = o.DVSwitchSetup(ctx); v.TGIF.PasswordSet || !v.DMR.PasswordSet {
		t.Fatalf("%+v %+v", v.DMR, v.TGIF)
	}
	// an unknown profile is refused
	if _, err := o.DVSwitchModes(ctx, &DVSwitchRequest{Modes: &DVModes{DMR: &on, DMRProfile: "dstar"}}); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
}

func TestDemoDMRProfiles(t *testing.T) {
	d := NewDemoDVSwitch()
	ctx := context.Background()
	d.installed, d.deps = true, true
	on := true
	pw := "example-pass"
	if _, err := d.DVSwitchCredentials(ctx, DVSwitchRequest{Credentials: &DVCredentials{Password: &pw, Profile: DVProfileTGIF}}); err != nil {
		t.Fatal(err)
	}
	res, err := d.DVSwitchModes(ctx, DVSwitchRequest{Modes: &DVModes{DMR: &on, DMRProfile: DVProfileTGIF}})
	if err != nil || !res.DMRProfileChanged {
		t.Fatalf("%+v %v", res, err)
	}
	v, _ := d.DVSwitchSetup(ctx)
	if !v.TGIF.Enabled || v.DMR.Enabled || !v.TGIF.PasswordSet || v.TGIF.Address != DefaultDVTGIFHost {
		t.Fatalf("%+v %+v", v.DMR, v.TGIF)
	}
	if _, err := d.DVSwitchModes(ctx, DVSwitchRequest{Modes: &DVModes{DMR: &on, DMRProfile: DVProfileOther}}); err != nil {
		t.Fatal(err)
	}
	if v, _ = d.DVSwitchSetup(ctx); v.TGIF.Enabled || !v.DMR.Enabled || !v.TGIF.PasswordSet {
		t.Fatalf("%+v %+v", v.DMR, v.TGIF)
	}
	// boot mode: the demo bridge starts in the last mode
	if _, err := d.DVSwitchBootMode(ctx, "YSF"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.DVSwitchRestart(ctx, DVSwitchRequest{Scope: "all"}); err != nil {
		t.Fatal(err)
	}
	if i, _ := d.DVSwitchInfo(ctx); i.Mode != "YSFN" || DVPanelMode(i) != "YSF" {
		t.Fatalf("%+v", i)
	}
	if _, err := d.DVSwitchBootMode(ctx, "DSTAR"); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
}
