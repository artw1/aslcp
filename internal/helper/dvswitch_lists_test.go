package helper

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readList(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseDMRHostsFixture(t *testing.T) {
	items := ParseDMRHosts(readList(t, "DMR_Hosts.txt"))
	// DMRGateway and DMR2YSF are loopback internals and are skipped.
	if len(items) != 5 {
		t.Fatalf("%d items: %+v", len(items), items)
	}
	it := items[2]
	if it.Name != "BM_2022_Greece" || it.Value != "2022.master.brandmeister.network" || it.Port != 62031 || it.Detail != "ID 2022" || it.Address != it.Value {
		t.Fatalf("%+v", it)
	}
	if x := items[0]; x.Name != "XLX_307" || x.Detail != "" || x.Port != 62030 {
		t.Fatalf("%+v", x)
	}
}

func TestParseDMRHostsSkipsBadLines(t *testing.T) {
	in := "# comment\n\n   \n" +
		"Good\t\t1234\t192.0.2.1\texample-pass\t62031\r\n" + // CRLF
		"Four 192.0.2.2 example-pass 62032\n" + // four fields: no network id
		"TooShort 192.0.2.3 62031\n" +
		"BadPort 1 192.0.2.4 example-pass 99999\n" +
		"ZeroPort 1 192.0.2.5 example-pass 0\n" +
		"TextPort 1 192.0.2.6 example-pass abc\n" +
		"BadHost 1 bad_host!;rm example-pass 62031\n" +
		"Loop 0 127.0.0.9 none 62031\n" +
		"Many 1 192.0.2.7 example-pass 62031 extra\n"
	items := ParseDMRHosts([]byte(in))
	if len(items) != 2 || items[0].Value != "192.0.2.1" || items[1].Value != "192.0.2.2" || items[1].Port != 62032 {
		t.Fatalf("%+v", items)
	}
}

func TestParseDMRHostsNeverKeepsPassword(t *testing.T) {
	items := ParseDMRHosts([]byte("Net 5 192.0.2.1 example-pass 62031\n"))
	if len(items) != 1 {
		t.Fatal(items)
	}
	if s := strings.Join([]string{items[0].Value, items[0].Name, items[0].Detail, items[0].Address}, "|"); strings.Contains(s, "example-pass") {
		t.Fatalf("password leaked: %s", s)
	}
}

func TestParseYSFHostsFixture(t *testing.T) {
	items := ParseYSFHosts(readList(t, "YSFHosts.txt"))
	if len(items) != 6 {
		t.Fatalf("%d items: %+v", len(items), items)
	}
	if it := items[0]; it.Value != "00006" || it.Name != "GR-HELLAS Zone A" || it.Detail != "(YCS202)" || it.Address != "46.59.68.211" || it.Port != 42000 {
		t.Fatalf("%+v", it)
	}
	if it := items[5]; it.Value != "00042" || it.Name != "US-0 FOX 0" || it.Detail != "YSF/DMR/ALL*" {
		t.Fatalf("%+v", it)
	}
}

func TestParseYSFHostsSkipsBadLines(t *testing.T) {
	in := "# c\n\n" +
		"00001;Good;Desc;192.0.2.1;42000;000;\n" +
		"1;ShortId;D;192.0.2.2;42000;000;\n" +
		"0000A;NotDigits;D;192.0.2.3;42000;000;\n" +
		"00002;Few;D;192.0.2.4\n" +
		"00003;BadPort;D;192.0.2.5;70000;000;\n" +
		"00004;BadHost;D;ho st;42000;000;\n" +
		"00005;;;192.0.2.6;42000\n" +
		"00006;Ctl\x01Name;D\x02;192.0.2.7;42000;000;\r\n"
	items := ParseYSFHosts([]byte(in))
	if len(items) != 3 {
		t.Fatalf("%+v", items)
	}
	if items[1].Name != "Reflector 00005" || items[2].Name != "CtlName" || items[2].Detail != "D" {
		t.Fatalf("%+v", items)
	}
}

func TestParseTGHostsFixtures(t *testing.T) {
	p := ParseTGHosts(readList(t, "P25Hosts.txt"), "P25")
	if len(p) != 7 || p[0].Value != "14" || p[0].Name != "P25 TG 14" || p[0].Address != "p25.xreflector.es" || p[0].Port != 41004 {
		t.Fatalf("%+v", p)
	}
	n := ParseTGHosts(readList(t, "NXDNHosts.txt"), "NXDN")
	if len(n) != 7 || n[0].Name != "NXDN TG 12" || n[0].Port != 41400 {
		t.Fatalf("%+v", n)
	}
}

func TestParseTGHostsSkipsBadLines(t *testing.T) {
	in := "# c\n\n10\t192.0.2.1\t41000\n" +
		"abc 192.0.2.2 41000\n" + // not a number
		"123456 192.0.2.3 41000\n" + // more than 5 digits
		"11 192.0.2.4\n" +
		"12 192.0.2.5 0\n" +
		"13 bad;host 41000\n" +
		"14 192.0.2.6 41000 extra\r\n"
	items := ParseTGHosts([]byte(in), "P25")
	if len(items) != 2 || items[0].Value != "10" || items[1].Value != "14" {
		t.Fatalf("%+v", items)
	}
}

func listOps(t *testing.T) *Ops {
	t.Helper()
	dir := t.TempDir()
	for n, src := range map[string]string{"DMR_Hosts.txt": "DMR_Hosts.txt", "YSFHosts.txt": "YSFHosts.txt", "P25Hosts.txt": "P25Hosts.txt", "NXDNHosts.txt": "NXDNHosts.txt"} {
		if err := os.WriteFile(filepath.Join(dir, n), readList(t, src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Ops{DVHostDir: dir}
}

func TestDVSwitchLists(t *testing.T) {
	o := listOps(t)
	ctx := context.Background()
	for mode, n := range map[string]int{"dmr": 5, "ysf": 6, "p25": 7, "nxdn": 7} {
		l, err := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: mode})
		if err != nil || l.Mode != mode || len(l.Items) != n || l.ETag == "" {
			t.Fatalf("%s: %+v %v", mode, l, err)
		}
	}
	a, _ := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: "ysf"})
	b, _ := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: "ysf"})
	c, _ := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: "p25"})
	if a.ETag != b.ETag || a.ETag == c.ETag {
		t.Errorf("etags %s %s %s", a.ETag, b.ETag, c.ETag)
	}
}

func TestDVSwitchListsModeIsAnEnum(t *testing.T) {
	o := listOps(t)
	for _, m := range []string{"", "DMR", "../etc/passwd", "dstar", "dmr/../ysf", "YSFHosts.txt"} {
		if _, err := o.DVSwitchLists(context.Background(), &DVSwitchRequest{Mode: m}); !errors.Is(err, ErrDVInvalid) {
			t.Errorf("mode %q: %v", m, err)
		}
	}
	if _, err := o.DVSwitchLists(context.Background(), nil); err == nil {
		t.Error("nil request accepted")
	}
}

func TestDVSwitchListsMissingAndUnsafeFiles(t *testing.T) {
	o := listOps(t)
	ctx := context.Background()
	os.Remove(filepath.Join(o.DVHostDir, "P25Hosts.txt"))
	if _, err := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: "p25"}); !errors.Is(err, ErrDVNoList) {
		t.Fatal(err)
	}
	// a symlink is not followed
	target := filepath.Join(t.TempDir(), "secret")
	os.WriteFile(target, []byte("10 192.0.2.1 41000\n"), 0o644)
	os.Symlink(target, filepath.Join(o.DVHostDir, "P25Hosts.txt"))
	if _, err := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: "p25"}); !errors.Is(err, ErrDVNoList) {
		t.Fatalf("symlink: %v", err)
	}
	// a directory is not a list
	os.Mkdir(filepath.Join(o.DVHostDir, "NXDNHosts.txt")+"x", 0o755)
	os.Remove(filepath.Join(o.DVHostDir, "NXDNHosts.txt"))
	os.Mkdir(filepath.Join(o.DVHostDir, "NXDNHosts.txt"), 0o755)
	if _, err := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: "nxdn"}); !errors.Is(err, ErrDVNoList) {
		t.Fatalf("dir: %v", err)
	}
	// over the size cap
	big := strings.Repeat("# padding padding padding padding padding\n", dvListFileMax/40+10)
	os.WriteFile(filepath.Join(o.DVHostDir, "DMR_Hosts.txt"), []byte(big), 0o644)
	if _, err := o.DVSwitchLists(ctx, &DVSwitchRequest{Mode: "dmr"}); !errors.Is(err, ErrDVNoList) {
		t.Fatalf("big: %v", err)
	}
}

func TestDVSwitchListsCap(t *testing.T) {
	o := listOps(t)
	var sb strings.Builder
	for i := 0; i < DVListMax+25; i++ {
		sb.WriteString("1 192.0.2.1 41000\n")
	}
	os.WriteFile(filepath.Join(o.DVHostDir, "P25Hosts.txt"), []byte(sb.String()), 0o644)
	l, err := o.DVSwitchLists(context.Background(), &DVSwitchRequest{Mode: "p25"})
	if err != nil || len(l.Items) != DVListMax || !l.Truncated {
		t.Fatalf("%d %v %v", len(l.Items), l.Truncated, err)
	}
	// an empty file is an empty list, not an error
	os.WriteFile(filepath.Join(o.DVHostDir, "P25Hosts.txt"), []byte("# nothing\n"), 0o644)
	l, err = o.DVSwitchLists(context.Background(), &DVSwitchRequest{Mode: "p25"})
	if err != nil || l.Items == nil || len(l.Items) != 0 {
		t.Fatalf("%+v %v", l, err)
	}
}

func TestDVListsOpDispatch(t *testing.T) {
	o := listOps(t)
	r, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchLists, DVSwitch: &DVSwitchRequest{Mode: "ysf"}})
	if err != nil {
		t.Fatal(err)
	}
	if l, ok := r.(DVList); !ok || len(l.Items) != 6 {
		t.Fatalf("%#v", r)
	}
	if _, err := o.HandleRequest(context.Background(), Request{Op: OpDVSwitchLists}); err == nil {
		t.Error("missing field accepted")
	}
	if !knownOp(OpDVSwitchLists) {
		t.Error("op is not known to the server")
	}
}

// The demo has its own placeholder lists in a temporary directory and does
// not read the real host dir.
func TestDemoListsUseTempDir(t *testing.T) {
	ops, err := NewDemoOps(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if ops.DVHostDir == "" || ops.DVHostDir == DefaultDVHostDir || strings.HasPrefix(ops.DVHostDir, DefaultDVHostDir) {
		t.Fatalf("host dir %q", ops.DVHostDir)
	}
	d := NewDemoDVSwitch()
	if _, err := d.DVSwitchLists(context.Background(), "dmr"); !errors.Is(err, ErrDVNoList) {
		t.Fatalf("no lists wired: %v", err)
	}
	if _, err := d.DVSwitchLists(context.Background(), "x"); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	d.Lists = ops
	for _, m := range DVListModes {
		l, err := d.DVSwitchLists(context.Background(), m)
		if err != nil || len(l.Items) < 3 {
			t.Fatalf("%s: %+v %v", m, l, err)
		}
		for _, it := range l.Items {
			if strings.Contains(it.Address, "brandmeister") || strings.Contains(it.Name, "example-pass") {
				t.Errorf("%+v", it)
			}
		}
	}
}

// ---- "use current as startup" in YSF mode

func abTune(t *testing.T, o *Ops, mode, tune string) {
	t.Helper()
	good, _ := os.ReadFile("testdata/ABInfo_31001.json")
	s := strings.Replace(string(good), `"ambe_mode":"DMR"`, `"ambe_mode":"`+mode+`"`, 1)
	if !strings.Contains(s, `"last_tune":""`) {
		t.Fatal("fixture has no empty last_tune: adjust the helper")
	}
	s = strings.Replace(s, `"last_tune":""`, `"last_tune":"`+tune+`"`, 1)
	os.RemoveAll(o.DVProcDir)
	abProc(t, o, "ABInfo_31001.json", []byte(s))
}

func ysfStartup(t *testing.T, o *Ops) string {
	t.Helper()
	v, err := o.DVSwitchSetup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return v.Gateways.YSF.Startup
}

func TestDVStartupUseCurrentYSFMapsHostPort(t *testing.T) {
	o, _ := cfgOps(t)
	o.DVHostDir = listOps(t).DVHostDir
	ctx := context.Background()
	abTune(t, o, "YSFN", "46.59.68.211:42000")
	res, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}})
	if err != nil || len(res.Warnings) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	if got := ysfStartup(t, o); got != "00006" {
		t.Fatalf("Startup = %q, want the reflector id", got)
	}
	// host match is case-insensitive; the port must match too
	abTune(t, o, "YSFW", "46.59.68.211:42001")
	res, err = o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}})
	if err != nil || len(res.Warnings) != 1 || ysfStartup(t, o) != "00006" {
		t.Fatalf("wrong port: %+v %v startup %q", res, err, ysfStartup(t, o))
	}
}

func TestDVStartupUseCurrentYSFNoMatchWarns(t *testing.T) {
	o, _ := cfgOps(t)
	o.DVHostDir = listOps(t).DVHostDir
	ctx := context.Background()
	before := ysfStartup(t, o)
	abTune(t, o, "YSFN", "192.0.2.99:42000")
	res, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "YSF") || strings.Contains(res.Warnings[0], "192.0.2.99") {
		t.Fatalf("warnings %v", res.Warnings)
	}
	if got := ysfStartup(t, o); got != before || strings.Contains(got, ":") {
		t.Fatalf("Startup changed to %q (was %q)", got, before)
	}
	// the other startup values were still applied
	v, _ := o.DVSwitchSetup(ctx)
	if v.Startup.AmbeMode != "YSFN" {
		t.Fatalf("ambeMode %q", v.Startup.AmbeMode)
	}
}

func TestDVStartupUseCurrentYSFWithoutListWarns(t *testing.T) {
	o, _ := cfgOps(t)
	o.DVHostDir = t.TempDir() // empty: no YSFHosts.txt
	abTune(t, o, "YSFN", "46.59.68.211:42000")
	res, err := o.DVSwitchStartup(context.Background(), &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}})
	if err != nil || len(res.Warnings) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestDVStartupUseCurrentYSFKeepsAnId(t *testing.T) {
	o, _ := cfgOps(t)
	o.DVHostDir = t.TempDir()
	abTune(t, o, "YSFN", "12345")
	if _, err := o.DVSwitchStartup(context.Background(), &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}}); err != nil {
		t.Fatal(err)
	}
	if got := ysfStartup(t, o); got != "12345" {
		t.Fatal(got)
	}
}

func TestDVStartupUseCurrentP25NXDNNeedNumbers(t *testing.T) {
	o, _ := cfgOps(t)
	ctx := context.Background()
	abTune(t, o, "P25", "10100")
	if res, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}}); err != nil || len(res.Warnings) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	abTune(t, o, "NXDN", "192.0.2.1:41400")
	res, err := o.DVSwitchStartup(ctx, &DVSwitchRequest{Startup: &DVStartup{UseCurrent: true}})
	if err != nil || len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "NXDN") {
		t.Fatalf("%+v %v", res, err)
	}
}
