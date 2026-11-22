package dvpanel

import "testing"

func TestNormModeAndFamily(t *testing.T) {
	for _, m := range PanelModes {
		if NormMode(m) != m {
			t.Errorf("%s not a panel mode", m)
		}
	}
	for _, m := range []string{"", "loop", "YSFN", "DSTAR", "bm"} {
		if NormMode(m) != "" {
			t.Errorf("%q accepted", m)
		}
	}
	for m, want := range map[string]bool{ModeBM: true, ModeTGIF: true, ModeDMR: true, ModeLoop: false, ModeYSF: false, ModeP25: false, ModeNXDN: false} {
		if DMRFamily(m) != want {
			t.Errorf("DMRFamily(%s)", m)
		}
	}
}

func TestValidTune(t *testing.T) {
	ok := map[string][]string{
		ModeBM:   {"1997", "1", "12345678", "9990#", "4000"},
		ModeTGIF: {"1998", "9990#"},
		ModeDMR:  {"1999"},
		ModeYSF:  {"12345", "192.0.2.10:42000", "node2001.example.net:42000", "Test Room", "disconnect"},
		ModeP25:  {"10100", "9999"},
		ModeNXDN: {"1", "65000"},
		ModeLoop: {"9990", "disconnect"},
	}
	for m, vs := range ok {
		for _, v := range vs {
			if !ValidTune(m, v) {
				t.Errorf("%s %q rejected", m, v)
			}
		}
	}
	bad := map[string][]string{
		ModeBM:   {"", "abc", "123456789", "9990##", "#9990", "-1", "1=2", "1 2"},
		ModeP25:  {"123456", "9990#", "abc"},
		ModeNXDN: {"1.5"},
		ModeYSF:  {"9990#", "a;b", "a#b", "-x", "x=y", " lead", "trail ", "192.0.2.10:0", "192.0.2.10:70000", "192.0.2.10:042"},
		ModeLoop: {"1997", "4000"},
		"XYZ":    {"1"},
	}
	for m, vs := range bad {
		for _, v := range vs {
			if ValidTune(m, v) {
				t.Errorf("%s %q accepted", m, v)
			}
		}
	}
}

func TestUnlinkAndPrivate(t *testing.T) {
	for m, want := range map[string]string{ModeBM: "4000", ModeTGIF: "4000", ModeDMR: "4000", ModeYSF: "disconnect", ModeP25: "9999", ModeNXDN: "9999", ModeLoop: "disconnect", "": ""} {
		if UnlinkTune(m) != want {
			t.Errorf("UnlinkTune(%q) = %q", m, UnlinkTune(m))
		}
	}
	if !UnlinkValue(ModeDMR, "4000") || UnlinkValue(ModeDMR, "1997") || UnlinkValue("XYZ", "") {
		t.Error("UnlinkValue")
	}
	if !PrivateTune(ModeBM, "9990#") || PrivateTune(ModeBM, "9990") || PrivateTune(ModeP25, "9990#") || PrivateTune(ModeBM, "#") {
		t.Error("PrivateTune")
	}
}
