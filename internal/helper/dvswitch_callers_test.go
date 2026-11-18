package helper

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDVPrivateTune(t *testing.T) {
	for _, c := range []struct {
		mode, tune string
		want       bool
	}{{"DMR", "9990#", true}, {"DMR", "9990", false}, {"DMR", "#", false}, {"P25", "9990#", false}} {
		if got := DVPrivateTune(c.mode, c.tune); got != c.want {
			t.Errorf("DVPrivateTune(%q,%q)=%v", c.mode, c.tune, got)
		}
	}
}

func TestDVTunePrivateSendsHash(t *testing.T) {
	o, ab := tuneEnv(t, "DMR", "")
	if _, err := o.DVSwitchTune(context.Background(), &DVSwitchRequest{Tune: &DVTune{Mode: "DMR", Tune: "9990#"}}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(ab.recv(), "|"); got != "txTg=9990#|info" {
		t.Fatalf("datagrams %q", got)
	}
}

func TestParseABInfoPrivate(t *testing.T) {
	info, err := ParseABInfo([]byte(`{"digital":{"tg":"9990","ts":"2"},"tlv":{"ambe_mode":"DMR","rx_port":"34001"},"last_tune":"9990#"}`))
	if err != nil || !info.Private || info.Tune != "" {
		t.Fatalf("%+v %v", info, err)
	}
	info, err = ParseABInfo([]byte(`{"digital":{"tg":"91","ts":"2"},"tlv":{"ambe_mode":"DMR","rx_port":"34001"},"last_tune":"91"}`))
	if err != nil || info.Private || info.Tune != "91" {
		t.Fatalf("%+v %v", info, err)
	}
}

func TestParseAnalogBridgeLine(t *testing.T) {
	ev, ok := parseAnalogBridgeLine("M: 2026-11-18 10:00:01.123 Begin TX: src=1234567 rpt=123456701 dst=91 slot=2 cc=0 call=N0CALL")
	if !ok || ev.End || ev.Caller.Call != "N0CALL" || ev.Caller.DMRID != "1234567" || ev.Caller.Dst != "91" || ev.Caller.Slot != 2 || ev.Caller.At != "2026-11-18 10:00:01" {
		t.Fatalf("%+v %v", ev, ok)
	}
	// the real shape: I: prefix, ms, 3-digit cc, empty call=
	ev, ok = parseAnalogBridgeLine("I: 2026-11-18 18:51:48.337 Begin TX: src=1234567 rpt=123456701 dst=3100 slot=1 cc=129 call=")
	if !ok || ev.Caller.Call != "" || ev.Caller.DMRID != "1234567" || ev.Caller.Dst != "3100" || ev.Caller.At != "2026-11-18 18:51:48" {
		t.Fatalf("%+v %v", ev, ok)
	}
	// other lines of the log are not events (there is no end line)
	for _, l := range []string{"I: 2026-11-18 18:51:48.337 txTg=: 3100", "I: 2026-11-18 18:51:48.337 INFO packet sent to USRP client", "M: 2026-11-18 18:52:00.000 PTT on", "M: 2026-11-18 18:52:03.000 PTT off (keyed for 2844 ms)", "2026-11-18 10:00:09 End TX: src=1234567"} {
		if _, ok := parseAnalogBridgeLine(l); ok {
			t.Errorf("parsed %q", l)
		}
	}
	for _, l := range []string{"", "random", "Begin TX: src=x call=N0CALL;rm", "Begin TX: src=1234567 rpt=1 dst=91 slot=2 cc=0 call=a b"} {
		if _, ok := parseAnalogBridgeLine(l); ok {
			t.Errorf("accepted %q", l)
		}
	}
}

func TestFoldCallers(t *testing.T) {
	b := func(call, dst string) DVCallerEvent {
		return DVCallerEvent{Caller: DVCaller{Call: call, DMRID: "1234567", Dst: dst, Slot: 2}}
	}
	end := DVCallerEvent{End: true}
	r := foldCallers([]DVCallerEvent{b("N0CALL", "91"), b("N0CALL", "91"), end, b("N0TEST", "91")})
	if r.Current == nil || r.Current.Call != "N0TEST" || len(r.Recent) != 2 || r.Recent[0].Call != "N0TEST" || r.Recent[1].Call != "N0CALL" {
		t.Fatalf("%+v", r)
	}
	r = foldCallers([]DVCallerEvent{b("N0CALL", "91"), end})
	if r.Current != nil || len(r.Recent) != 1 {
		t.Fatalf("%+v", r)
	}
	var evs []DVCallerEvent
	for _, d := range []string{"1", "2", "3", "4", "5", "6", "7"} {
		evs = append(evs, b("N0CALL", d), end)
	}
	if r = foldCallers(evs); len(r.Recent) != dvCallersRecent || r.Recent[0].Dst != "7" {
		t.Fatalf("%+v", r)
	}
}

func TestDVCollectCallersLog(t *testing.T) {
	o, _ := cfgOps(t)
	dir := t.TempDir()
	o.DVBridgeLogDir = dir
	log := filepath.Join(dir, "Analog_Bridge.log")
	// padding pushes the first line out of the bounded tail
	pad := strings.Repeat("M: 2026-11-18 09:00:00 noise line\n", 20000)
	body := "M: 2026-11-18 08:00:00 Begin TX: src=1234567 rpt=1 dst=1 slot=2 cc=0 call=N0OLD\n" + pad +
		"M: 2026-11-18 10:00:01 Begin TX: src=1234567 rpt=123456701 dst=91 slot=2 cc=0 call=N0CALL\n"
	if err := os.WriteFile(log, []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	c := o.dvCollectCallers("")
	if c.Current == nil || c.Current.Call != "N0CALL" || len(c.Recent) != 1 {
		t.Fatalf("%+v", c)
	}
	// a symlinked log is refused
	other := filepath.Join(t.TempDir(), "x")
	if err := os.WriteFile(other, []byte("M: 2026-11-18 10:00:01 Begin TX: src=1234567 rpt=1 dst=91 slot=2 cc=0 call=N0EVIL\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	os.Remove(log)
	if err := os.Symlink(other, log); err != nil {
		t.Fatal(err)
	}
	if c := o.dvCollectCallers(""); c.Current != nil || len(c.Recent) != 0 {
		t.Fatalf("symlink followed: %+v", c)
	}
	// missing log: empty, not an error
	o.DVBridgeLogDir = filepath.Join(dir, "none")
	if c := o.dvCollectCallers(""); c.Current != nil || c.Recent == nil {
		t.Fatalf("%+v", c)
	}
}

func TestDVIdentityInfo(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	info := &DVInfo{Location: "Example Town", Description: "Test hotspot", URL: "https://example.org", FrequencyHz: 434000000, Latitude: "51.5", Longitude: "-0.12"}
	res, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01", Info: info}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Files) == 0 {
		t.Fatalf("%+v", res)
	}
	ini := readDV(t, o, DVFileMMDVMBridge)
	for _, w := range []string{"RXFrequency=434000000", "TXFrequency=434000000", "Location=Example Town", "Latitude=51.5", "Longitude=-0.12"} {
		if !strings.Contains(ini, w) {
			t.Errorf("missing %q", w)
		}
	}
	v, err := o.DVSwitchSetup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if iv := v.Identity.Info; iv.Location != "Example Town" || iv.FrequencyHz != 434000000 || iv.Stock {
		t.Fatalf("%+v", iv)
	}
	for name, bad := range map[string]DVInfo{
		"location":    {Location: "a;b", Description: "d", URL: "https://example.org", FrequencyHz: 434000000},
		"description": {Location: "ok", Description: strings.Repeat("x", 20), URL: "https://example.org", FrequencyHz: 434000000},
		"url":         {Location: "ok", Description: "d", URL: "javascript:alert(1)", FrequencyHz: 434000000},
		"frequencyHz": {Location: "ok", Description: "d", URL: "https://example.org", FrequencyHz: 5},
		"latitude":    {Location: "ok", Description: "d", URL: "https://example.org", FrequencyHz: 434000000, Latitude: "91", Longitude: "0"},
		"longitude":   {Location: "ok", Description: "d", URL: "https://example.org", FrequencyHz: 434000000, Latitude: "0", Longitude: "181"},
	} {
		bad := bad
		before := readDV(t, o, DVFileMMDVMBridge)
		if _, err := o.DVSwitchIdentity(ctx, &DVSwitchRequest{Identity: &DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01", Info: &bad}}); err == nil || !strings.Contains(err.Error(), "identity/info/"+name) {
			t.Errorf("%s: %v", name, err)
		}
		if readDV(t, o, DVFileMMDVMBridge) != before {
			t.Errorf("%s: file changed", name)
		}
	}
}
