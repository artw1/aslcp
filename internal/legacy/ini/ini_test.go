package ini

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var stock = []string{"Analog_Bridge.ini", "MMDVM_Bridge.ini", "YSFGateway.ini", "P25Gateway.ini", "NXDNGateway.ini", "Bridge.ini"}

func read(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestRoundTripStock(t *testing.T) {
	for _, n := range stock {
		b := read(t, n)
		if got := Parse(b).Bytes(); !bytes.Equal(got, b) {
			t.Errorf("%s: round trip differs", n)
		}
	}
	for _, s := range []string{"", "\n", "a", "[x]", "[x]\r\nk=v\r\n", "\xef\xbb\xbf[x]\nk=v", "k = v ; c\n\n\n", "=\n[\n]\n"} {
		if got := string(Parse([]byte(s)).Bytes()); got != s {
			t.Errorf("round trip %q -> %q", s, got)
		}
	}
}

// lineDiff returns the 1-based numbers of the lines that differ (same count).
func lineDiff(t *testing.T, a, b []byte) []int {
	t.Helper()
	la, lb := strings.Split(string(a), "\n"), strings.Split(string(b), "\n")
	if len(la) != len(lb) {
		t.Fatalf("line count %d != %d", len(la), len(lb))
	}
	var d []int
	for i := range la {
		if la[i] != lb[i] {
			d = append(d, i+1)
		}
	}
	return d
}

func TestSetChangesOnlyTheValue(t *testing.T) {
	b := read(t, "Analog_Bridge.ini")
	f := Parse(b)
	ch, err := f.Set("AMBE_AUDIO", "gatewayDmrId", "7654321")
	if err != nil || !ch {
		t.Fatal(ch, err)
	}
	out := f.Bytes()
	if d := lineDiff(t, b, out); len(d) != 1 {
		t.Fatalf("changed lines %v", d)
	}
	// spacing, alignment and the inline comment are kept
	if !strings.Contains(string(out), "gatewayDmrId = 7654321                  ; ID to use when transmitting") {
		t.Fatalf("style lost:\n%s", out)
	}
	if v, _ := Parse(out).Get("AMBE_AUDIO", "gatewayDmrId"); v != "7654321" {
		t.Fatal(v)
	}
	// the same key in another section is untouched
	if v, _ := Parse(out).Get("DV3000", "rxPort"); v != "2460" {
		t.Fatal(v)
	}
}

func TestSetTabBeforeComment(t *testing.T) {
	f := Parse([]byte("[GENERAL]\ntransferRootDir = /tmp\t\t\t; Export\n"))
	if v, _ := f.Get("GENERAL", "transferRootDir"); v != "/tmp" {
		t.Fatalf("%q", v)
	}
	f.Set("GENERAL", "transferRootDir", "/run/x")
	if got := string(f.Bytes()); got != "[GENERAL]\ntransferRootDir = /run/x\t\t\t; Export\n" {
		t.Fatalf("%q", got)
	}
}

func TestMMDVMStyle(t *testing.T) {
	b := read(t, "MMDVM_Bridge.ini")
	f := Parse(b)
	for _, c := range [][3]string{{"General", "Callsign", "N0CALL"}, {"DMR Network", "Port", "62031"}} {
		if v, ok := f.Get(c[0], c[1]); !ok || v != c[2] {
			t.Fatalf("%v: %q %v", c, v, ok)
		}
	}
	f.Set("General", "Callsign", "N0CALL/P")
	f.Set("DMR Network", "Password", "example-pass")
	f.Set("DMR", "Enable", "1")
	f.Set("DMR Network", "Enable", "1")
	out := string(f.Bytes())
	for _, w := range []string{"Callsign=N0CALL/P\n", "Password=example-pass\n", "[DMR]\nEnable=1\n", "[DMR Network]\nEnable=1\n"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q", w)
		}
	}
	if d := lineDiff(t, b, f.Bytes()); len(d) != 4 {
		t.Fatalf("changed lines %v", d)
	}
	// same-named keys of other sections are untouched
	if v, _ := f.Get("NXDN", "Id"); v != "12345" {
		t.Fatal(v)
	}
	if v, _ := f.Get("General", "Id"); v != "1234567" {
		t.Fatal(v)
	}
}

func TestSetNoChange(t *testing.T) {
	f := Parse(read(t, "MMDVM_Bridge.ini"))
	if ch, err := f.Set("General", "Id", "1234567"); err != nil || ch {
		t.Fatal(ch, err)
	}
}

func TestAppendBelowCommentedExample(t *testing.T) {
	b := read(t, "YSFGateway.ini")
	f := Parse(b)
	if _, ok := f.Get("Network", "Startup"); ok {
		t.Fatal("stock has a commented Startup only")
	}
	ch, err := f.Set("Network", "Startup", "192.0.2.10:42000")
	if err != nil || !ch {
		t.Fatal(ch, err)
	}
	if !strings.Contains(string(f.Bytes()), "[Network]\n# Startup=Alabama-Link\nStartup=192.0.2.10:42000\nInactivityTimeout=0\n") {
		t.Fatalf("%s", f.Bytes())
	}
	if v, _ := Parse(f.Bytes()).Get("Network", "Startup"); v != "192.0.2.10:42000" {
		t.Fatal(v)
	}
	// only one line was added
	if len(bytes.Split(f.Bytes(), []byte("\n"))) != len(bytes.Split(b, []byte("\n")))+1 {
		t.Fatal("more than one line added")
	}
}

func TestAppendAfterLastKey(t *testing.T) {
	f := Parse([]byte("[A]\nx = 1\ny = 2\n\n# trailing note\n\n[B]\nz=3\n"))
	f.Set("A", "w", "9")
	if got := string(f.Bytes()); got != "[A]\nx = 1\ny = 2\nw = 9\n\n# trailing note\n\n[B]\nz=3\n" {
		t.Fatalf("%q", got)
	}
	// empty section: right below the header, "=" style
	f = Parse([]byte("[A]\n[B]\nz=3\n"))
	f.Set("A", "w", "9")
	if got := string(f.Bytes()); got != "[A]\nw=9\n[B]\nz=3\n" {
		t.Fatalf("%q", got)
	}
}

func TestAppendKeepsLineEndings(t *testing.T) {
	f := Parse([]byte("[A]\r\nx=1\r\n[B]\r\n"))
	f.Set("A", "y", "2")
	if got := string(f.Bytes()); got != "[A]\r\nx=1\r\ny=2\r\n[B]\r\n" {
		t.Fatalf("%q", got)
	}
	// last line without newline
	f = Parse([]byte("[A]\nx=1"))
	f.Set("A", "y", "2")
	if got := string(f.Bytes()); got != "[A]\nx=1\ny=2" {
		t.Fatalf("%q", got)
	}
}

func TestMissingSectionIsError(t *testing.T) {
	f := Parse(read(t, "P25Gateway.ini"))
	before := f.Bytes()
	if _, err := f.Set("Nope", "k", "v"); err != ErrNoSection {
		t.Fatal(err)
	}
	if !bytes.Equal(before, f.Bytes()) {
		t.Fatal("file changed")
	}
}

func TestRejectsDangerousValues(t *testing.T) {
	f := Parse([]byte("[A]\nx=1\n"))
	for _, v := range []string{"a\nb=c", "a\rb", "a\x00", " lead", "trail ", "a ;c", "a\t;c", ";x", "a\x7f"} {
		if _, err := f.Set("A", "x", v); err == nil {
			t.Errorf("%q accepted", v)
		}
	}
	for _, k := range []string{"", "a=b", "a b", "[x]", "a\nb", "#k"} {
		if _, err := f.Set("A", k, "1"); err == nil {
			t.Errorf("key %q accepted", k)
		}
	}
	if string(f.Bytes()) != "[A]\nx=1\n" {
		t.Fatal("changed")
	}
	// '#' and ';' without a blank before are plain value characters
	if _, err := f.Set("A", "x", "a;b#c"); err != nil {
		t.Fatal(err)
	}
}

func TestCommentedAndEmptyValues(t *testing.T) {
	f := Parse([]byte("[A]\n# Options=\nOptions=\n"))
	if v, ok := f.Get("A", "Options"); !ok || v != "" {
		t.Fatal(v, ok)
	}
	f.Set("A", "Options", "x")
	if string(f.Bytes()) != "[A]\n# Options=\nOptions=x\n" {
		t.Fatalf("%q", f.Bytes())
	}
}

func TestBOMAndDuplicates(t *testing.T) {
	f := Parse([]byte("\xef\xbb\xbf[A]\nk=1\n[A]\nk=2\n"))
	f.Set("A", "k", "9")
	if string(f.Bytes()) != "\xef\xbb\xbf[A]\nk=9\n[A]\nk=2\n" {
		t.Fatalf("%q", f.Bytes())
	}
}

func TestSectionHeaderWithComment(t *testing.T) {
	f := Parse([]byte("[STFU]      ; note\nBMPort = 1\n"))
	if !f.HasSection("STFU") {
		t.Fatal("no section")
	}
	f.Set("STFU", "BMPort", "2")
	if string(f.Bytes()) != "[STFU]      ; note\nBMPort = 2\n" {
		t.Fatalf("%q", f.Bytes())
	}
}

func TestGetFold(t *testing.T) {
	f := Parse([]byte("[DMR]\ntxPort = 31100 ; c\nrxPort  = 31103\n"))
	if v, ok := f.GetFold("DMR", "TXPort"); !ok || v != "31100" {
		t.Fatalf("%q %v", v, ok)
	}
	if v, ok := f.GetFold("DMR", "RXPORT"); !ok || v != "31103" {
		t.Fatalf("%q %v", v, ok)
	}
	if _, ok := f.GetFold("dmr", "txPort"); ok {
		t.Fatal("section must stay case-sensitive")
	}
	if _, ok := f.Get("DMR", "TXPort"); ok {
		t.Fatal("Get must stay case-sensitive")
	}
}

func TestKeyFold(t *testing.T) {
	f := Parse([]byte("[STFU]   ; comment\ntxPort  = 36100 ; c\n"))
	if k := f.KeyFold("STFU", "TXPORT"); k != "txPort" {
		t.Fatalf("%q", k)
	}
	if k := f.KeyFold("STFU", "UserID"); k != "UserID" {
		t.Fatalf("%q", k)
	}
	if _, err := f.Set("STFU", f.KeyFold("STFU", "TxPort"), "36200"); err != nil {
		t.Fatal(err)
	}
	if got := string(f.Bytes()); got != "[STFU]   ; comment\ntxPort  = 36200 ; c\n" {
		t.Fatalf("%q", got)
	}
}
