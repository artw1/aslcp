package astconf

import "testing"

func TestSectionRename(t *testing.T) {
	in := "; head\n[1999](node-main)  ; the node\nrxchannel=SimpleUSB/usb\n\n[usb]\nfoo=bar\r\n"
	f, _ := Parse("rpt.conf", []byte(in))
	s := f.Section("1999")[0]
	s.Rename("2001")
	if s.Name != "2001" || s.Header.Name != "2001" {
		t.Fatalf("name not updated: %q", s.Name)
	}
	want := "; head\n[2001](node-main)  ; the node\nrxchannel=SimpleUSB/usb\n\n[usb]\nfoo=bar\r\n"
	if got := string(f.Bytes()); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	if len(f.Section("2001")) != 1 || len(f.Section("1999")) != 0 {
		t.Fatalf("lookup not updated")
	}
	// refused renames leave the file alone
	for _, bad := range []string{"", "a]b", "x\ny", "2001"} {
		s.Rename(bad)
	}
	f.Sections()[0].Rename("zzz")
	if got := string(f.Bytes()); got != want {
		t.Fatalf("refused rename changed file: %q", got)
	}
}
