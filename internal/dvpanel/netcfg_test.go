package dvpanel

import "testing"

func TestEnableBlockDefaultPassword(t *testing.T) {
	id := IdentityState{Callsign: "N0CALL", DMRID: 1234567}
	st := NetState{Address: "192.0.2.10", Port: 62031}
	dmr, _ := NetSpecFor(ModeDMR)
	if why := EnableBlock(dmr, st, id); why != "" {
		t.Errorf("DMR - Other without a password must be enabled with the default: %q", why)
	}
	for _, m := range []string{ModeBM, ModeTGIF} {
		sp, _ := NetSpecFor(m)
		s := st
		if m == ModeTGIF {
			s.Address = ""
		}
		if why := EnableBlock(sp, s, id); why != "set the password first" {
			t.Errorf("%s without a password: %q", m, why)
		}
	}
}

func TestTGIFHasNoOptionsOrSlot(t *testing.T) {
	for _, o := range OptSpecs(ModeTGIF) {
		if o.Key == "options" || o.Key == "slot" {
			t.Errorf("TGIF lists %s", o.Key)
		}
	}
	has := map[string]bool{}
	for _, o := range OptSpecs(ModeDMR) {
		has[o.Key] = true
	}
	if !has["options"] || !has["slot"] {
		t.Error("DMR - Other lost options or slot")
	}
}

func TestM17Settings(t *testing.T) {
	sp, ok := NetSpecFor(ModeM17)
	if !ok || sp.Address || sp.Port || sp.Password || !NativeMode(ModeM17) {
		t.Fatalf("%+v %v", sp, ok)
	}
	o, err := CleanOptions(ModeM17, map[string]string{"can": " 7 ", "suffix": "r", "timeout_ms": ""})
	if err != nil || o["can"] != "7" || o["suffix"] != "r" || len(o) != 2 {
		t.Fatalf("%v %v", o, err)
	}
	for _, bad := range []map[string]string{{"can": "16"}, {"can": "-1"}, {"module": "A"}, {"suffix": "toolong"}} {
		if _, err := CleanOptions(ModeM17, bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	if _, err := CheckUpdate(ModeM17, NetUpdate{Options: map[string]string{"suffix": "$"}}); err == nil {
		t.Error("suffix $ accepted")
	}
	if _, err := CheckUpdate(ModeM17, NetUpdate{Options: map[string]string{"suffix": "-R", "can": "3"}}); err != nil {
		t.Error(err)
	}
	if _, err := CheckUpdate(ModeM17, NetUpdate{Address: new(string)}); err == nil {
		t.Error("address accepted")
	}
	id := IdentityState{Callsign: "N0CALL", DMRID: 1234567}
	if why := EnableBlock(sp, NetState{}, id); why != "" {
		t.Error(why)
	}
	if why := EnableBlock(sp, NetState{Options: map[string]string{"suffix": "ABCDEF"}}, id); why == "" {
		t.Error("unencodable callsign enabled")
	}
	if why := EnableBlock(sp, NetState{}, IdentityState{}); why == "" {
		t.Error("enabled without identity")
	}
}

func TestBMStayLoggedInOption(t *testing.T) {
	var sp OptSpec
	for _, o := range OptSpecs(ModeBM) {
		if o.Key == "stay_logged_in" {
			sp = o
		}
	}
	if sp.Kind != KindBool || sp.Default != "false" || sp.Advanced || sp.Help == "" {
		t.Fatalf("%+v", sp)
	}
	u, err := CheckUpdate(ModeBM, NetUpdate{Options: map[string]string{"stay_logged_in": "true"}})
	if err != nil || u.Options["stay_logged_in"] != "true" {
		t.Fatalf("%+v %v", u, err)
	}
	if _, err := CheckUpdate(ModeTGIF, NetUpdate{Options: map[string]string{"stay_logged_in": "true"}}); err == nil {
		t.Error("TGIF took the BM option")
	}
}
