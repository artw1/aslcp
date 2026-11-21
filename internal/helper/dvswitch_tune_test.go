package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestDVValidTunePerMode(t *testing.T) {
	cases := []struct {
		mode, tune string
		ok         bool
	}{
		{"DMR", "91", true}, {"DMR", "12345678", true}, {"DMR", "123456789", false}, {"DMR", "", false}, {"DMR", "9a", false}, {"DMR", "-1", false},
		{"DMR", "4000", true}, {"DMR", "91;rm", false}, {"DMR", "txTg=1", false},
		{"DMR", "9990#", true}, {"DMR", "#", false}, {"DMR", "9990##", false}, {"DMR", "#9990", false}, {"P25", "9990#", false}, {"YSF", "9990#", false},
		{"P25", "10100", true}, {"P25", "100000", false}, {"P25", "abc", false}, {"P25", "1=2", false},
		{"NXDN", "10200", true}, {"NXDN", "192.0.2.1:41400", false}, {"NXDN", "", false},
		{"YSF", "192.0.2.10:42000", true}, {"YSF", "ysf.example.org:42000", true}, {"YSF", "192.0.2.10:99999", false},
		{"YSF", "12345", true}, {"YSF", "US-Example", true}, {"YSF", "disconnect", true},
		{"YSF", "-12345", false}, {"YSF", "a=b", false}, {"YSF", "a;b", false}, {"YSF", "a#b", false}, {"YSF", " lead", false}, {"YSF", "", false},
		{"DSTAR", "1", false}, {"", "1", false}, {"dmr", "1", false},
	}
	for _, c := range cases {
		if got := DVValidTune(c.mode, c.tune); got != c.ok {
			t.Errorf("DVValidTune(%q, %q) = %v, want %v", c.mode, c.tune, got, c.ok)
		}
	}
}

func TestDVNormModeAndUnlink(t *testing.T) {
	for in, want := range map[string]string{"DMR": "DMR", "YSFN": "YSF", "YSFW": "YSF", "YSF": "YSF", "P25": "P25", "NXDN": "NXDN", "DSTAR": "", "": ""} {
		if got := DVNormMode(in); got != want {
			t.Errorf("DVNormMode(%q) = %q, want %q", in, got, want)
		}
	}
	if !DVUnlinkValue("DMR", "4000") || !DVUnlinkValue("YSF", "disconnect") || !DVUnlinkValue("P25", "9999") || DVUnlinkValue("DMR", "91") {
		t.Fatal("unlink values")
	}
}

func TestDVInfoStarted(t *testing.T) {
	o, f := cfgOps(t)
	f.rules["systemctl show -p MainPID"] = resp{out: "MainPID=4242\nActiveEnterTimestamp=@1795000000\n"}
	good, _ := os.ReadFile("testdata/ABInfo_31001.json")
	abProc(t, o, "ABInfo_31001.json", good)
	info, err := o.DVSwitchInfo(context.Background())
	if err != nil || !info.Found || info.Started != time.Unix(1795000000, 0).UTC().Format(time.RFC3339) {
		t.Fatalf("%+v %v", info, err)
	}
	// running, but no state file yet: not found, yet the start time is known
	os.RemoveAll(o.DVProcDir)
	info, _ = o.DVSwitchInfo(context.Background())
	if info.Found || info.Started == "" {
		t.Fatalf("%+v", info)
	}
	// a timestamp in another format leaves the start unknown
	f.rules["systemctl show -p MainPID"] = resp{out: "MainPID=4242\nActiveEnterTimestamp=Tue 2026-11-17 18:42:00 CET\n"}
	abProc(t, o, "ABInfo_31001.json", good)
	if info, _ = o.DVSwitchInfo(context.Background()); !info.Found || info.Started != "" {
		t.Fatalf("%+v", info)
	}
	// not running
	f.rules["systemctl show -p MainPID"] = resp{out: "MainPID=0\nActiveEnterTimestamp=@1795000000\n"}
	if info, _ = o.DVSwitchInfo(context.Background()); info.Found || info.Started != "" {
		t.Fatalf("%+v", info)
	}
}

// tuneEnv: an Ops whose ABInfo names the TLV port of a fake Analog_Bridge
// (dvswitch_ab_fake_test.go) that really moves its listener on rxport=.
func tuneEnv(t *testing.T, mode, lastTune string) (*Ops, *fakeAB) {
	t.Helper()
	o, _ := cfgOps(t)
	// BrandMeister's card enabled: a BM tune is allowed
	if err := o.dvBMEnabledSet(true); err != nil {
		t.Fatal(err)
	}
	// and DMR - Other (seeded from the fixture's [DMR Network])
	netOn(t, o, DVModeDMR)
	return o, newFakeAB(t, o, mode, lastTune)
}

func TestDVTuneSameModeSendsTxTg(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "91"}})
	if err != nil || res.Switched || res.Mode != "DMR" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := strings.Join(ab.recv(), "|"); got != "txTg=91|info" {
		t.Fatalf("datagrams %q", got)
	}
}

func TestDVTuneSwitchSequence(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", Tune: "10100"}})
	if err != nil || !res.Switched {
		t.Fatalf("%+v %v", res, err)
	}
	want := ab.switchCmds("P25") + "|txTg=10100|info"
	if got := strings.Join(ab.recv(), "|"); got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
}

func TestDVTuneYSFResolvesIDAndName(t *testing.T) {
	o, ab := tuneEnv(t, "YSFN", "")
	o.DVHostDir = listOps(t).DVHostDir
	for _, v := range []string{"00006", "46.59.68.211:42000"} {
		if _, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "YSF", Tune: v}}); err != nil {
			t.Fatal(v, err)
		}
		if got := ab.recv(); len(got) != 2 || got[0] != "txTg=46.59.68.211:42000" {
			t.Fatalf("%s: %v", v, got)
		}
	}
	// an id that is not in the list is refused, nothing is sent
	_, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "YSF", Tune: "99999"}})
	if !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	if got := ab.recv(); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestDVTuneRefusals(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	ctx := context.Background()
	for _, tc := range []struct {
		req *DVSwitchRequest
		err error
	}{
		{&DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "9x"}}, ErrDVInvalid},
		{&DVSwitchRequest{Tune: &DVTune{Mode: "DSTAR", Tune: "9"}}, ErrDVInvalid},
		{&DVSwitchRequest{Tune: &DVTune{Mode: "P25", Tune: "txTg=1"}}, ErrDVInvalid},
	} {
		if _, err := o.DVSwitchTune(ctx, tc.req); !errors.Is(err, tc.err) {
			t.Errorf("%+v: %v", tc.req.Tune, err)
		}
	}
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{}); err == nil {
		t.Error("missing tune accepted")
	}
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "9"}, Modes: &DVModes{}}); err == nil {
		t.Error("extra field accepted")
	}
	if got := ab.recv(); len(got) != 0 {
		t.Fatalf("sent %v", got)
	}
	// no mode ports: the switch is refused before anything is sent
	if err := os.WriteFile(filepath.Join(o.dvOptDir(), "MMDVM_Bridge", "DVSwitch.ini"), []byte("[DMR]\ntxPort=31103\nrxPort=31100\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "NXDN", Tune: "10200"}}); !errors.Is(err, ErrDVNoPorts) {
		t.Fatal(err)
	}
	if got := ab.recv(); len(got) != 0 {
		t.Fatalf("sent %v", got)
	}
	// no ABInfo: no_info
	os.RemoveAll(o.DVProcDir)
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "9"}}); !errors.Is(err, ErrDVNoInfo) {
		t.Fatal(err)
	}
}

func TestDVTuneOpDispatch(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	out, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchTune, DVSwitch: &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "262"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := out.(DVTuneResult); !ok || r.Mode != "DMR" {
		t.Fatalf("%#v", out)
	}
	if len(ab.recv()) != 2 {
		t.Fatal("no datagrams")
	}
}

func TestDVUnlinkTune(t *testing.T) {
	for mode, want := range map[string]string{"DMR": "4000", "YSF": "disconnect", "P25": "9999", "NXDN": "9999"} {
		if got := DVUnlinkTune(mode); got != want || !DVUnlinkValue(mode, got) {
			t.Errorf("DVUnlinkTune(%s) = %q", mode, got)
		}
	}
	if DVUnlinkTune("DSTAR") != "" {
		t.Error("unknown mode has an unlink value")
	}
}

func TestDVTuneModeOnlySwitch(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", ModeOnly: true}})
	if err != nil || !res.Switched || res.Mode != "P25" {
		t.Fatalf("%+v %v", res, err)
	}
	want := ab.switchCmds("P25")
	if got := strings.Join(ab.recv(), "|"); got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
}

func TestDVTuneModeOnlySameModeIsQuiet(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", ModeOnly: true}})
	if err != nil || res.Switched || !res.Confirmed {
		t.Fatalf("%+v %v", res, err)
	}
	if got := ab.recv(); len(got) != 0 {
		t.Fatalf("datagrams %q", got)
	}
}

func TestDVTuneModeOnlyRefusesATarget(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	_, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "YSF", Tune: "12345", ModeOnly: true}})
	if !errors.Is(err, ErrDVInvalid) || len(ab.recv()) != 0 {
		t.Fatalf("err %v", err)
	}
}

func TestDemoDVSwitchModeOnly(t *testing.T) {
	d := NewDemoDVSwitch()
	d.installed = true
	ctx := context.Background()
	before, _ := d.DVSwitchInfo(ctx)
	other := "P25"
	if DVNormMode(before.Mode) == other {
		other = "NXDN"
	}
	res, err := d.DVSwitchTune(ctx, DVSwitchRequest{Tune: &DVTune{Mode: other, ModeOnly: true}})
	if err != nil || !res.Switched || !res.Confirmed {
		t.Fatalf("%+v %v", res, err)
	}
	after, _ := d.DVSwitchInfo(ctx)
	if DVNormMode(after.Mode) != other || after.Tune != before.Tune {
		t.Fatalf("before %+v after %+v", before, after)
	}
	if _, err := d.DVSwitchTune(ctx, DVSwitchRequest{Tune: &DVTune{Mode: "DSTAR", ModeOnly: true}}); err == nil {
		t.Error("unknown mode accepted")
	}
	// unlink value goes through the normal tune path
	if _, err := d.DVSwitchTune(ctx, DVSwitchRequest{Tune: &DVTune{Mode: other, Tune: DVUnlinkTune(other)}}); err != nil {
		t.Errorf("unlink: %v", err)
	}
}

// Regression: the stock DVSwitch.ini spells the keys txPort/rxPort and
// dvswitch.sh reads them case-insensitively (configparser).
func TestDVModePortsStockSpelling(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	fix, err := os.ReadFile("testdata/DVSwitch.ini")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.dvOptDir(), "MMDVM_Bridge", "DVSwitch.ini"), fix, 0o644); err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[string][2]int{"DMR": {31100, 31103}, "P25": {32100, 32103}, "STFU": {36100, 36103}} {
		tx, rx, err := o.dvModePorts(mode)
		if err != nil || tx != want[0] || rx != want[1] {
			t.Fatalf("%s: %d %d %v", mode, tx, rx, err)
		}
	}
	if _, _, err := o.dvModePorts("YSF"); !errors.Is(err, ErrDVNoPorts) {
		t.Fatalf("missing section: %v", err)
	}
	// the tune itself needs ports the fake can bind: the same file with free ones
	p25 := ab.ports["P25"]
	fix = []byte(strings.NewReplacer("32100", strconv.Itoa(p25[0]), "32103", strconv.Itoa(p25[1])).Replace(string(fix)))
	if err := os.WriteFile(filepath.Join(o.dvOptDir(), "MMDVM_Bridge", "DVSwitch.ini"), fix, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", Tune: "10100"}}); err != nil {
		t.Fatal(err)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("P25")+"|txTg=10100|info"; got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
}

// One call does the whole switch although Analog_Bridge moves its listener at
// every rxport=: the commands after it must go to the new port.
func TestDVTuneSwitchFollowsListener(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", Tune: "10100"}})
	if err != nil || !res.Switched || !res.Confirmed {
		t.Fatalf("%+v %v", res, err)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("P25")+"|txTg=10100|info"; got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
	if mode, tune, port := ab.state(); mode != "P25" || tune != "10100" || port != ab.ports["P25"][0] {
		t.Fatalf("AB is in %s %s on %d", mode, tune, port)
	}
}

func TestDVTuneBackToBackSwitches(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	ctx := context.Background()
	for i, m := range []string{"YSF", "P25", "NXDN", "DMR", "P25", "YSF"} {
		res, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: m, ModeOnly: true}})
		if err != nil || !res.Switched || !res.Confirmed {
			t.Fatalf("%d %s: %+v %v", i, m, res, err)
		}
		if mode, _, _ := ab.state(); DVNormMode(mode) != m {
			t.Fatalf("%d: AB is in %s, want %s", i, mode, m)
		}
	}
	// a tune right after a switch lands too
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "91"}}); err != nil {
		t.Fatal(err)
	}
	if _, tune, _ := ab.state(); tune != "91" {
		t.Fatalf("tune %q", tune)
	}
}

// Concurrent requests are serialised, none fails, and AB ends in the mode of
// the one that finished last.
func TestDVTuneConcurrent(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	modes := []string{"YSF", "P25", "NXDN", "DMR", "YSF", "P25", "NXDN", "YSF"}
	var mu sync.Mutex
	var order []string
	var wg sync.WaitGroup
	for _, m := range modes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: m, ModeOnly: true}})
			if err != nil || !res.Confirmed {
				t.Errorf("%s: %+v %v", m, res, err)
			}
			mu.Lock()
			order = append(order, m)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if mode, _, _ := ab.state(); DVNormMode(mode) != order[len(order)-1] {
		t.Fatalf("AB is in %s, the last request done was %s (%v)", mode, order[len(order)-1], order)
	}
}

// A waiting request gives up with its context instead of failing early.
func TestDVTuneWaitsBoundedByContext(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	unlock, err := o.lockTune(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "P25", ModeOnly: true}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if got := ab.recv(); len(got) != 0 {
		t.Fatalf("sent %v", got)
	}
	unlock()
	if res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", ModeOnly: true}}); err != nil || !res.Confirmed {
		t.Fatalf("%+v %v", res, err)
	}
}

// ABInfo never shows the new port: the helper sends to the commanded port
// after the wait, which is where AB was told to listen.
func TestDVTunePortFallbackWhenABInfoStale(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	ab.freezeInfo()
	o.DVPortWait = 40 * time.Millisecond
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", ModeOnly: true}})
	if err != nil || !res.Switched || !res.Confirmed {
		t.Fatalf("%+v %v", res, err)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("P25"); got != want {
		t.Fatalf("received %q, want %q", got, want)
	}
}

// A DMR talkgroup is the whole string in digits: no prefix is taken from a
// reflector address or any other text.
func TestDVStrictDMRTG(t *testing.T) {
	for v, ok := range map[string]bool{
		"3100": true, "9": true, "12345678": true,
		"192.0.2.10:42000": false, "192": true, "192abc": false, "3100 ": false, " 3100": false,
		"3100\n": false, "31 00": false, "123456789": false, "-3100": false, "3100=1": false, "٣١٠٠": false, "": false,
	} {
		if got := DVValidTune("DMR", v); got != ok {
			t.Errorf("DVValidTune(DMR, %q) = %v, want %v", v, got, ok)
		}
	}
	info, err := ParseABInfo([]byte(`{"digital":{"tg":"192.0.2.10:42000","ts":"2"},"tlv":{"ambe_mode":"DMR","rx_port":"34001"}}`))
	if err == nil && info.TG != "" {
		t.Errorf("tg %q taken from an address", info.TG)
	}
}

// A switch away from a linked mode first sends that mode's unlink value
// while Analog_Bridge still points at its partner, then switches.
func TestDVTuneSwitchUnlinksTheModeLeft(t *testing.T) {
	ctx := context.Background()
	o, ab := tuneEnv(t, "DMR", "")
	stfu := strconv.Itoa(ab.ports["STFU"][1])

	// DMR (unlinked: tg 4000) to BM 91: nothing to leave
	res, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "BM", Tune: "91"}})
	if err != nil || !res.Switched || res.Mode != "BM" || res.Left != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("BM")+"|txTg=91|info"; got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
	if got := strings.Join(ab.sent(), ","); got != "91>"+stfu {
		t.Fatalf("sent %q", got)
	}
	info, _ := o.DVSwitchInfo(ctx)
	if DVPanelMode(info) != DVModeBM {
		t.Fatalf("panel %q", info.Panel)
	}

	// BM on 91 to DMR: 4000 goes to STFU first, then the switch
	res, err = o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "4000"}})
	if err != nil || !res.Switched || res.Mode != "DMR" || res.Left != DVModeBM || res.LeftFailed {
		t.Fatalf("%+v %v", res, err)
	}
	if got, want := strings.Join(ab.recv(), "|"), "txTg=4000|"+ab.switchCmds("DMR")+"|txTg=4000|info"; got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
	dmr := strconv.Itoa(ab.ports["DMR"][1])
	if got := strings.Join(ab.sent(), ","); got != "4000>"+stfu+",4000>"+dmr {
		t.Fatalf("sent %q", got)
	}

	// back to BM 91: DMR is unlinked, no extra datagram
	res, err = o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "BM", Tune: "91"}})
	if err != nil || res.Left != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("BM")+"|txTg=91|info"; got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
}

func TestDVTuneSwitchUnlinksYSF(t *testing.T) {
	ctx := context.Background()
	o, ab := tuneEnv(t, "YSFN", "192.0.2.10:42000")
	res, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "4000"}})
	if err != nil || !res.Switched || res.Left != "YSF" {
		t.Fatalf("%+v %v", res, err)
	}
	if got, want := strings.Join(ab.recv(), "|"), "txTg=disconnect|"+ab.switchCmds("DMR")+"|txTg=4000|info"; got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
	// an unlinked YSF is left without one
	o, ab = tuneEnv(t, "YSFN", "disconnect")
	res, err = o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "P25", ModeOnly: true}})
	if err != nil || res.Left != "" {
		t.Fatalf("%+v %v", res, err)
	}
	if got, want := strings.Join(ab.recv(), "|"), ab.switchCmds("P25"); got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
}
