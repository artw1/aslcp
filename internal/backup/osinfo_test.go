package backup

import "testing"

func TestReleaseLabel(t *testing.T) {
	if ReleaseLabel("trixie") != "Debian 13 (trixie)" || ReleaseLabel("sid") != "sid" || DebianNumber("bookworm") != "12" {
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
