package helper

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
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

// tuneEnv: an Ops whose ABInfo names a loopback UDP port as the TLV port, and
// the datagrams Analog_Bridge would receive.
func tuneEnv(t *testing.T, mode, lastTune string) (*Ops, func() []string) {
	t.Helper()
	o, _ := cfgOps(t)
	o.DVTLVDelay, o.DVTLVSettle = time.Millisecond, time.Millisecond
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Skip("no loopback UDP:", err)
	}
	t.Cleanup(func() { pc.Close() })
	port := pc.LocalAddr().(*net.UDPAddr).Port
	good, _ := os.ReadFile("testdata/ABInfo_31001.json")
	s := strings.Replace(string(good), `"rx_port":"31100"`, `"rx_port":"`+strconv.Itoa(port)+`"`, 1)
	s = strings.Replace(s, `"ambe_mode":"DMR"`, `"ambe_mode":"`+mode+`"`, 1)
	s = strings.Replace(s, `"last_tune":""`, `"last_tune":"`+lastTune+`"`, 1)
	abProc(t, o, "ABInfo_31001.json", []byte(s))
	ini := "[DMR]\nTXPort=31103\nRXPort=31100\n\n[YSF]\nTXPort=31203\nRXPort=31200\n\n[P25]\nTXPort=31303\nRXPort=31300\n"
	p := filepath.Join(o.dvOptDir(), "MMDVM_Bridge", "DVSwitch.ini")
	if err := os.WriteFile(p, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
	return o, func() []string {
		var got []string
		buf := make([]byte, 512)
		for {
			_ = pc.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
			n, _, err := pc.ReadFrom(buf)
			if err != nil {
				return got
			}
			if n < 2 || buf[0] != 0x05 || int(buf[1]) != n-2 {
				got = append(got, "BAD")
				continue
			}
			got = append(got, string(buf[2:n]))
		}
	}
}

func TestDVTuneSameModeSendsTxTg(t *testing.T) {
	o, recv := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "91"}})
	if err != nil || res.Switched || res.Mode != "DMR" {
		t.Fatalf("%+v %v", res, err)
	}
	if got := strings.Join(recv(), "|"); got != "txTg=91|info" {
		t.Fatalf("datagrams %q", got)
	}
}

func TestDVTuneSwitchSequence(t *testing.T) {
	o, recv := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", Tune: "10100"}})
	if err != nil || !res.Switched {
		t.Fatalf("%+v %v", res, err)
	}
	want := "rxport=30000|ambeMode=P25|txport=31300|rxport=31303|info|txTg=10100|info"
	if got := strings.Join(recv(), "|"); got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
}

func TestDVTuneYSFResolvesIDAndName(t *testing.T) {
	o, recv := tuneEnv(t, "YSFN", "")
	o.DVHostDir = listOps(t).DVHostDir
	for _, v := range []string{"00006", "46.59.68.211:42000"} {
		if _, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "YSF", Tune: v}}); err != nil {
			t.Fatal(v, err)
		}
		if got := recv(); len(got) != 2 || got[0] != "txTg=46.59.68.211:42000" {
			t.Fatalf("%s: %v", v, got)
		}
	}
	// an id that is not in the list is refused, nothing is sent
	_, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "YSF", Tune: "99999"}})
	if !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	if got := recv(); len(got) != 0 {
		t.Fatal(got)
	}
}

func TestDVTuneRefusals(t *testing.T) {
	o, recv := tuneEnv(t, "DMR", "")
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
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "9"}, Startup: &DVStartup{}}); err == nil {
		t.Error("extra field accepted")
	}
	if got := recv(); len(got) != 0 {
		t.Fatalf("sent %v", got)
	}
	// no mode ports: the switch is refused before anything is sent
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "NXDN", Tune: "10200"}}); !errors.Is(err, ErrDVNoPorts) {
		t.Fatal(err)
	}
	if got := recv(); len(got) != 0 {
		t.Fatalf("sent %v", got)
	}
	// no ABInfo: no_info
	os.RemoveAll(o.DVProcDir)
	if _, err := o.DVSwitchTune(ctx, &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "9"}}); !errors.Is(err, ErrDVNoInfo) {
		t.Fatal(err)
	}
}

func TestDVTuneOpDispatch(t *testing.T) {
	o, recv := tuneEnv(t, "DMR", "")
	out, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchTune, DVSwitch: &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "262"}}})
	if err != nil {
		t.Fatal(err)
	}
	if r, ok := out.(DVTuneResult); !ok || r.Mode != "DMR" {
		t.Fatalf("%#v", out)
	}
	if len(recv()) != 2 {
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
	o, recv := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "P25", ModeOnly: true}})
	if err != nil || !res.Switched || res.Mode != "P25" {
		t.Fatalf("%+v %v", res, err)
	}
	want := "rxport=30000|ambeMode=P25|txport=31300|rxport=31303|info"
	if got := strings.Join(recv(), "|"); got != want {
		t.Fatalf("datagrams %q, want %q", got, want)
	}
}

func TestDVTuneModeOnlySameModeIsQuiet(t *testing.T) {
	o, recv := tuneEnv(t, "DMR", "")
	res, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", ModeOnly: true}})
	if err != nil || res.Switched || !res.Confirmed {
		t.Fatalf("%+v %v", res, err)
	}
	if got := recv(); len(got) != 0 {
		t.Fatalf("datagrams %q", got)
	}
}

func TestDVTuneModeOnlyRefusesATarget(t *testing.T) {
	o, recv := tuneEnv(t, "DMR", "")
	_, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "YSF", Tune: "12345", ModeOnly: true}})
	if !errors.Is(err, ErrDVInvalid) || len(recv()) != 0 {
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
