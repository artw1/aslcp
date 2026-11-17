package backup

import "testing"

func TestCodenameFromVersion(t *testing.T) {
	for in, want := range map[string]string{
		"22.10.1+asl3-3.10.8-1.deb12": "bookworm",
		"22.10.1+asl3-3.10.8-1.deb13": "trixie",
		"3.11.0-1.deb14":              "forky",
		"3.10.8-1.deb12u1":            "bookworm",
		"3.10.8-1.deb99":              "",
		"3.10.8-1.deb123":             "",
		"3.10.8-1":                    "",
		"":                            "",
	} {
		if got := CodenameFromVersion(in); got != want {
			t.Errorf("CodenameFromVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReleaseOf(t *testing.T) {
	if got := ReleaseOf("trixie", "1.deb12", ""); got != "trixie" {
		t.Errorf("explicit codename must win, got %q", got)
	}
	if got := ReleaseOf("", "", "22.1-1.deb13"); got != "trixie" {
		t.Errorf("asterisk fallback, got %q", got)
	}
	if got := ReleaseOf("", "1.deb12", "22.1-1.deb13"); got != "bookworm" {
		t.Errorf("asl3 before asterisk, got %q", got)
	}
	if ReleaseLabel("trixie") != "Debian 13 (trixie)" || ReleaseLabel("sid") != "sid" {
		t.Error("ReleaseLabel")
	}
}

func TestParseOSRelease(t *testing.T) {
	o := ParseOSRelease([]byte("PRETTY_NAME=\"Debian GNU/Linux 13 (trixie)\"\nNAME=\"Debian GNU/Linux\"\nVERSION_CODENAME=trixie\nID=debian\n# c\nJUNK\nHOME_URL='x'\n"))
	if o.ID != "debian" || o.Codename != "trixie" || o.Pretty != "Debian GNU/Linux 13 (trixie)" {
		t.Errorf("got %+v", o)
	}
	if o := ParseOSRelease(nil); o != (OSInfo{}) {
		t.Errorf("empty: %+v", o)
	}
}

func TestManifestOSFields(t *testing.T) {
	m := baseManifest()
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest without OS fields: %v", err)
	}
	m.OSID, m.OSVersionCodename, m.OSPrettyName = "debian", "trixie", "Debian GNU/Linux 13 (trixie)"
	if err := m.Validate(); err != nil {
		t.Fatalf("manifest with OS fields: %v", err)
	}
	m.OSPrettyName = string(make([]byte, 300))
	if m.Validate() == nil {
		t.Error("overlong osName accepted")
	}
	m.OSPrettyName = "ok"
	m.OSVersionCodename = "bad\xff"
	if m.Validate() == nil {
		t.Error("invalid UTF-8 osCodename accepted")
	}
}
