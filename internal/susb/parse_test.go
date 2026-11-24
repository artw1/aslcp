package susb

import (
	"errors"
	"strings"
	"testing"
)

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestParseParams(t *testing.T) {
	// The reference node's real reply (anonymised values only).
	p, err := ParseParams([]string{"500,0,0,0,1,1,1,0,4,0,0,0,650,31,37,35\r"})
	if err != nil {
		t.Fatal(err)
	}
	want := Params{TxA: 500, PreEmphasis: true, DeEmphasis: true, PlFilter: true, CarrierFrom: "usbinvert", CtcssFrom: "no",
		RxVoice: 650, MicPlayMax: 31, SpkrMax: 37, MicMax: 35}
	if p != want {
		t.Fatalf("%+v", p)
	}
	// delays are converted from frames to milliseconds
	p, err = ParseParams([]string{"1,2,1,1,0,0,0,1,6,5,10,25,999,1,2,3"})
	if err != nil || p.RxOnDelayMs != 200 || p.TxOffDelayMs != 500 || !p.PttInvert || !p.Echo || p.CarrierFrom != "ppinvert" || p.CtcssFrom != "ppinvert" {
		t.Fatalf("%+v %v", p, err)
	}
	// field count must be exactly 16
	for _, bad := range []string{
		"500,0,0,0,1,1,1,0,4,0,0,0",                // the plain "0" form
		"500,0,0,0,1,1,1,0,4,0,0,0,650,31,37,35,1", // 17
		"500,0,0,0,1,1,1,0,4,0,0,0,650,31,37",      // 15
	} {
		if _, err := ParseParams([]string{bad}); code(err) != CodeUnsupported {
			t.Errorf("%q: %v", bad, err)
		}
	}
	for _, bad := range [][]string{nil, {""}, {"hello"}, {"Unable to find command"},
		{"1000,0,0,0,1,1,1,0,4,0,0,0,650,31,37,35"}, // level out of range
		{"500,0,2,0,1,1,1,0,4,0,0,0,650,31,37,35"},  // not a boolean
		{"500,0,0,0,1,1,1,0,4,0,3001,0,650,31,37,35"},
	} {
		if _, err := ParseParams(bad); code(err) != CodeUnexpected {
			t.Errorf("%q: %v", bad, err)
		}
	}
	if _, err := ParseParams([]string{"Device 1999 is selected, the associated USB device string 1-1 was not found"}); code(err) != CodeDeviceMissing {
		t.Errorf("not found: %v", err)
	}
	// an N/A code is reported, not mapped to a valid mode
	p, err = ParseParams([]string{"1,2,0,0,0,0,0,0,1,3,0,0,5,1,2,3"})
	if err != nil || !p.CarrierOther || !p.CtcssOther || p.CarrierFrom != "other" {
		t.Fatalf("%+v %v", p, err)
	}
	if q := PatchFrom(p); q.CarrierFrom != nil || q.CtcssFrom != nil {
		t.Fatal("an unknown signal source would be written back")
	}
}

func TestParseMeter(t *testing.T) {
	m, err := ParseMeter([]string{"RxAudioStats: Pk -76.3  Avg Pwr -89  Min -90  Max -87  dBFS  ClipCnt 0"})
	if err != nil || m.Peak != -76.3 || m.Avg != -89 || m.Min != -90 || m.Max != -87 || m.Clip != 0 {
		t.Fatalf("%+v %v", m, err)
	}
	m, err = ParseMeter([]string{"RxAudioStats: Pk  -2.1  Avg Pwr -32  Min -60  Max -12  dBFS  ClipCnt 7\r"})
	if err != nil || m.Peak != -2.1 || m.Clip != 7 {
		t.Fatalf("%+v %v", m, err)
	}
	m, err = ParseMeter([]string{"RxAudioStats: Pk -15.6  Avg Pwr -34  Min -60  Max -27  dBFS  ClipCnt 0"})
	if err != nil || m.Peak != -15.6 {
		t.Fatalf("%+v %v", m, err)
	}
	if _, err := ParseMeter([]string{"Rx not keyed"}); code(err) != CodeUnexpected {
		t.Fatal(err)
	}
}

func TestParseDevices(t *testing.T) {
	d, err := ParseDevices([]string{"1999,1998\r"})
	if err != nil || len(d) != 2 || d[0] != "1999" || d[1] != "1998" {
		t.Fatalf("%v %v", d, err)
	}
	if d, err = ParseDevices(nil); err != nil || len(d) != 0 {
		t.Fatalf("%v %v", d, err)
	}
	if d, err = ParseDevices([]string{""}); err != nil || len(d) != 0 {
		t.Fatalf("%v %v", d, err)
	}
	if _, err = ParseDevices([]string{"No such command 'susb tune menu-support 1' (type 'help susb' for other possible commands)"}); code(err) != CodeNoDriver {
		t.Fatalf("%v", err)
	}
	if _, err = ParseDevices([]string{"1999;rm -rf"}); code(err) != CodeUnexpected {
		t.Fatalf("%v", err)
	}
}

func TestActiveAndIdentity(t *testing.T) {
	if n, err := ParseActive([]string{"Active Simple USB Radio device is [1999]."}); err != nil || n != "1999" {
		t.Fatal(n, err)
	}
	if _, err := ParseActive([]string{"nonsense"}); code(err) != CodeUnexpected {
		t.Fatal(err)
	}
	if err := VerifySetActive("1999", []string{"Active (command) Simple USB Radio device set to [1999]"}); err != nil {
		t.Fatal(err)
	}
	if err := VerifySetActive("1999", []string{"Active (command) Simple USB Radio device set to [1998]"}); code(err) != CodeUnexpected {
		t.Fatalf("wrong device accepted: %v", err)
	}
	if err := VerifySetActive("1999", []string{"No device [1999] exists"}); code(err) != CodeUnknownDevice {
		t.Fatal(err)
	}
	if err := VerifySetActive("1999", nil); code(err) != CodeUnexpected {
		t.Fatal(err)
	}
	id, err := ParseIdentity("1999", []string{"Active radio interface is [1999]", "Device String is 1-1.2", "Device Serial is ABC", "Card is 3", "Rx Level currently set to 650"})
	if err != nil || id.String != "1-1.2" || id.Serial != "ABC" || id.Card != 3 {
		t.Fatalf("%+v %v", id, err)
	}
	if _, err = ParseIdentity("1999", []string{"Active radio interface is [1998]"}); code(err) != CodeUnexpected {
		t.Fatalf("wrong device accepted: %v", err)
	}
	if _, err = ParseIdentity("1999", []string{"Device 1999 is selected, the associated USB device string 1-1 was not found"}); code(err) != CodeDeviceMissing {
		t.Fatal(err)
	}
	a := ParseAssignments([]string{"Device [1999] is assigned to device=1-1.2 card=3", "junk"})
	if len(a) != 1 || a[0].Device != "1999" || a[0].Path != "1-1.2" || a[0].Card != 3 {
		t.Fatalf("%+v", a)
	}
}

func TestCodeWhitelist(t *testing.T) {
	for n, c := range map[string]int{"no": 0, "usb": 3, "usbinvert": 4, "pp": 5, "ppinvert": 6} {
		if got, ok := CarrierCode(n); !ok || got != c {
			t.Errorf("carrier %s", n)
		}
	}
	for n, c := range map[string]int{"no": 0, "usb": 1, "usbinvert": 2, "pp": 4, "ppinvert": 5} {
		if got, ok := CtcssCode(n); !ok || got != c {
			t.Errorf("ctcss %s", n)
		}
	}
	for _, bad := range []string{"", "N/A", "1", "7", "-1", "usb;", "USB", "other"} {
		s := bad
		if _, err := (Patch{CarrierFrom: &s}).Specs("1999"); code(err) != CodeInvalid {
			t.Errorf("carrier %q accepted", bad)
		}
		if _, err := (Patch{CtcssFrom: &s}).Specs("1999"); code(err) != CodeInvalid {
			t.Errorf("ctcss %q accepted", bad)
		}
	}
	// the codes that go on the wire are only the whitelisted ones
	for _, n := range SignalNames {
		n := n
		sp, err := (Patch{CarrierFrom: &n, CtcssFrom: &n}).Specs("1999")
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range sp {
			if !Allowed(s.cmd) {
				t.Errorf("%q not allowed", s.cmd)
			}
		}
	}
}

func TestMsToFrames(t *testing.T) {
	for ms, want := range map[int]int{0: 0, 19: 1, 20: 1, 30: 2, 100: 5, 1000: 50, 60000: 3000} {
		if got, err := MsToFrames(ms); err != nil || got != want {
			t.Errorf("%d ms: %d %v", ms, got, err)
		}
	}
	for _, bad := range []int{-1, -20, 60001, 1 << 30} {
		if _, err := MsToFrames(bad); err == nil {
			t.Errorf("%d accepted", bad)
		}
	}
	if FramesToMs(50) != 1000 {
		t.Fatal("FramesToMs")
	}
}

func TestLevelValidation(t *testing.T) {
	for _, v := range []int{-1, 1000, 100000} {
		v := v
		for _, p := range []Patch{{RxVoice: &v}, {TxA: &v}, {TxB: &v}} {
			if _, err := p.Specs("1999"); code(err) != CodeInvalid {
				t.Errorf("%d accepted", v)
			}
		}
	}
	for _, v := range []int{0, 500, 999} {
		v := v
		sp, err := (Patch{RxVoice: &v, TxA: &v, TxB: &v}).Specs("1999")
		if err != nil || len(sp) != 3 {
			t.Fatal(err)
		}
	}
}

func TestAllowedCommands(t *testing.T) {
	for _, ok := range []string{
		"susb active", "susb active 1999", "susb active show", "susb tune save",
		"susb tune menu-support 0+4", "susb tune menu-support 1", "susb tune menu-support 2", "susb tune menu-support Y",
		"susb tune menu-support c650", "susb tune menu-support f0", "susb tune menu-support g999",
		"susb tune menu-support k1", "susb tune menu-support r3", "susb tune menu-support s5", "susb tune menu-support t150",
	} {
		if !Allowed(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{
		"susb tune menu-support fK500", "susb tune menu-support gK", "susb tune menu-support l", "susb tune menu-support b",
		"susb tune menu-support v", "susb tune menu-support y", "susb tune menu-support z", "susb tune menu-support j",
		"susb tune menu-support r1", "susb tune menu-support r2", "susb tune menu-support r7", "susb tune menu-support s3",
		"susb tune menu-support s6", "susb tune menu-support k2", "susb tune menu-support c-1", "susb tune menu-support c1000",
		"susb tune load", "susb tune swap 1998", "susb tune rxcap", "susb tune txcap", "susb tune nocap", "susb tune rxd",
		"susb active 1999;quit", "susb active a b", "core restart now", "susb tune menu-support c5\nHangup", "",
	} {
		if Allowed(bad) {
			t.Errorf("%q allowed", bad)
		}
	}
}

func TestReplyVerification(t *testing.T) {
	v := 650
	sp, _ := (Patch{RxVoice: &v}).Specs("1999")
	if _, err := sp[0].VerifyReply([]string{"Channel 1999: Changed setting on RX Channel to 650"}); err != nil {
		t.Fatal(err)
	}
	// a reply from another device is rejected
	if _, err := sp[0].VerifyReply([]string{"Channel 1998: Changed setting on RX Channel to 650"}); err == nil {
		t.Fatal("reply of another device accepted")
	}
	if _, err := sp[0].VerifyReply([]string{"Channel 1999: Changed setting on RX Channel to 640"}); err == nil {
		t.Fatal("different value accepted")
	}
	_, err := sp[0].VerifyReply([]string{"Channel 1999: Entry Error, Rx Channel Level setting not changed"})
	if code(err) != CodeCommandFailed || !strings.Contains(err.Error(), "Entry Error") {
		t.Fatal(err)
	}
	if _, err := sp[0].VerifyReply(nil); code(err) != CodeUnexpected {
		t.Fatal(err)
	}
	on := true
	sp, _ = (Patch{RxBoost: &on, PttInvert: &on}).Specs("1999")
	if _, err := sp[0].VerifyReply([]string{"RxBoost changed to Enabled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := sp[0].VerifyReply([]string{"RxBoost changed to Disabled"}); err == nil {
		t.Fatal("wrong state accepted")
	}
	if _, err := sp[1].VerifyReply([]string{"PTT mode changed to Open"}); err != nil {
		t.Fatal(err)
	}
	car := "usb"
	sp, _ = (Patch{CarrierFrom: &car}).Specs("1999")
	if _, err := sp[0].VerifyReply([]string{"Carrier From changed to N/A"}); err == nil {
		t.Fatal("N/A accepted")
	}
	if _, err := sp[0].VerifyReply([]string{"Carrier From changed to usb"}); err != nil {
		t.Fatal(err)
	}
}

func TestDiffIgnoresEcho(t *testing.T) {
	a := Params{RxVoice: 1, Echo: true, MicMax: 3}
	b := Params{RxVoice: 2}
	if d := Diff(a, b); len(d) != 1 || d[0] != "rxVoice" {
		t.Fatalf("%v", d)
	}
}

func TestDeviceNotFoundReply(t *testing.T) {
	reply := []string{"Device 1999 is selected, the associated USB device string 1-1.1.3:1.0 was not found\r"}
	want := "the USB audio interface of 1999 (USB port 1-1.1.3) was not found: it is unplugged, or Asterisk cannot use it yet. If ASL3 was installed recently, reboot the Pi."
	check := func(name string, err error) {
		t.Helper()
		var e *Error
		if !errors.Is(err, ErrDeviceNotFound) || !errors.As(err, &e) {
			t.Fatalf("%s: %v", name, err)
		}
		if e.Code != CodeDeviceMissing || e.Device != "1999" || e.DevStr != "1-1.1.3:1.0" || e.Msg != want {
			t.Fatalf("%s: %+v", name, e)
		}
	}
	_, err := ParseMeter(reply)
	check("meter", err)
	_, err = ParseParams(reply)
	check("params", err)
	_, err = ParseIdentity("1999", reply)
	check("identity", err)
	_, err = ParseActive(reply)
	check("active", err)
	if err := VerifySetActive("1999", reply); !errors.Is(err, ErrDeviceNotFound) {
		t.Fatal(err)
	}
	if _, err := ParseMeter([]string{"Rx not keyed"}); errors.Is(err, ErrDeviceNotFound) {
		t.Fatal("unrelated reply matched")
	}
}
