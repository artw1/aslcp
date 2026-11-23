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
