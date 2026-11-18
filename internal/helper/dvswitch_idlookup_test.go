package helper

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

const dvIDFixture = "# comment\n\n1234567\tN0CALL\tTest Name\n1234568 N0DEMO Demo Op\n1234569,N0TEST,Other\n0000042 N0ZERO\nbad line\n12345678901 N0LONG\n"

func idOps(t *testing.T, ini string) (*Ops, string) {
	t.Helper()
	dir := t.TempDir()
	o := &Ops{DVHostDir: dir, DVOptDir: t.TempDir()}
	if ini != "" {
		writeIDIni(t, o, ini)
	}
	p := filepath.Join(dir, "DMRIds.dat")
	if err := os.WriteFile(p, []byte(dvIDFixture), 0o644); err != nil {
		t.Fatal(err)
	}
	return o, p
}

func writeIDIni(t *testing.T, o *Ops, ini string) {
	t.Helper()
	p := filepath.Join(o.DVOptDir, "MMDVM_Bridge", "MMDVM_Bridge.ini")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(ini), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDVLookupCalls(t *testing.T) {
	o, p := idOps(t, "")
	got := o.dvLookupCalls([]string{"1234567", "1234568", "1234569", "42", "999", "12345678901"})
	want := map[string]string{"1234567": "N0CALL", "1234568": "N0DEMO", "1234569": "N0TEST", "42": "N0ZERO"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: %q", k, got[k])
		}
	}
	// a changed file is reloaded (size/mtime); earlier misses were cached
	if err := os.WriteFile(p, []byte("999 N0NEW\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.Chtimes(p, time.Now(), time.Now().Add(time.Hour))
	if got := o.dvLookupCalls([]string{"999", "1234567"}); got["999"] != "N0NEW" || got["1234567"] != "" {
		t.Fatalf("%v", got)
	}
	// a symlink is refused
	_ = os.Remove(p)
	_ = os.Symlink("/etc/passwd", p)
	if got := o.dvLookupCalls([]string{"1"}); len(got) != 0 {
		t.Fatalf("%v", got)
	}
}

func TestDVIDFilePath(t *testing.T) {
	def := func(o *Ops) string { return filepath.Join(o.DVHostDir, "DMRIds.dat") }
	o, _ := idOps(t, "[DMR Id Lookup]\nFile=relative.dat\n")
	if got := o.dvIDFile(); got != def(o) {
		t.Fatal(got)
	}
	o, _ = idOps(t, "[DMR Id Lookup]\nFile=/etc/shadow\n")
	if got := o.dvIDFile(); got != def(o) {
		t.Fatal(got)
	}
	o, _ = idOps(t, "")
	alt := filepath.Join(o.DVHostDir, "Other.dat")
	ini := "[DMR Id Lookup]\nFile=" + alt + "\nTime=24\n"
	writeIDIni(t, o, ini)
	if got := o.dvIDFile(); got != alt {
		t.Fatal(got)
	}
}

func TestDVResolveCallers(t *testing.T) {
	o, _ := idOps(t, "")
	evs := []DVCallerEvent{
		{Caller: DVCaller{DMRID: "1234567", Dst: "3100"}},
		{Caller: DVCaller{Call: "N0LOG", DMRID: "1234568", Dst: "3100"}},
		{Caller: DVCaller{DMRID: "777", Dst: "3100"}},
		{Caller: DVCaller{DMRID: "9990", Dst: "1234567"}},
		{Caller: DVCaller{DMRID: "1234569", Dst: "1234567"}},
	}
	dvResolveCallers(o, evs, "1234567")
	c := func(i int) DVCaller { return evs[i].Caller }
	if c(0).Call != "N0CALL" || c(1).Call != "N0LOG" || c(2).Call != "" || c(2).DMRID != "777" {
		t.Fatalf("%+v", evs)
	}
	if !c(3).Private || c(3).Call != "" || !c(4).Private || c(4).Call != "" || c(0).Private {
		t.Fatalf("%+v", evs)
	}
}

func TestDVCollectCallersLookup(t *testing.T) {
	o, _ := idOps(t, "")
	o.DVBridgeLogDir = t.TempDir()
	body := "I: 2026-11-18 18:51:40.000 txTg=: 9990\n" +
		"I: 2026-11-18 18:51:41.000 Begin TX: src=9990 rpt=123456701 dst=1234567 slot=1 cc=129 call=\n" +
		"I: 2026-11-18 18:51:44.000 txTg=: 3100\n" +
		"I: 2026-11-18 18:51:48.337 Begin TX: src=1234567 rpt=123456701 dst=3100 slot=1 cc=1 call=\n" +
		"I: 2026-11-18 18:51:49.000 INFO packet sent to USRP client\n" +
		"I: 2026-11-18 18:52:00.000 Begin TX: src=777 rpt=123456701 dst=3100 slot=1 cc=1 call=\n" +
		"I: 2026-11-18 18:52:10.000 Begin TX: src=1234568 rpt=123456701 dst=3100 slot=1 cc=1 call=N0LOG\n"
	if err := os.WriteFile(filepath.Join(o.DVBridgeLogDir, "Analog_Bridge.log"), []byte(body), 0o640); err != nil {
		t.Fatal(err)
	}
	c := o.dvCollectCallers("1234567")
	if len(c.Recent) != 4 || c.Current == nil || c.Current.Call != "N0LOG" {
		t.Fatalf("%+v", c)
	}
	// newest first: N0LOG (log call= kept), 777 (unknown: ID only), N0CALL (lookup), Parrot (private)
	r := c.Recent
	if r[1].Call != "" || r[1].DMRID != "777" || r[2].Call != "N0CALL" || !r[3].Private || r[3].Call != "" || r[3].DMRID != "9990" {
		t.Fatalf("%+v", r)
	}
}
