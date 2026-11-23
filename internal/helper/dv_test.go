package helper

import (
	"context"
	"encoding/json"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	dvctl "github.com/artw1/aslcp/internal/dv/ctl"
)

func TestDVPackagesByArch(t *testing.T) {
	if got := dvPackages("armhf"); len(got) != 1 || got[0] != dvMD380Pkg {
		t.Fatalf("armhf: %v", got)
	}
	if got := dvPackages("arm64"); len(got) != 2 || got[1] != dvQemuPkg {
		t.Fatalf("arm64: %v", got)
	}
}

func TestDVARMv6Refused(t *testing.T) {
	dir := t.TempDir()
	write := func(model string) *Ops {
		p := filepath.Join(dir, "cpuinfo")
		os.WriteFile(p, []byte("processor\t: 0\nmodel name\t: "+model+"\n"), 0o644)
		return &Ops{DVCpuInfo: p}
	}
	if !write("ARMv6-compatible processor rev 7 (v6l)").dvARMv6() {
		t.Fatal("v6l not detected")
	}
	if write("ARMv7 Processor rev 4 (v7l)").dvARMv6() {
		t.Fatal("v7l detected as v6")
	}
	if (&Ops{DVCpuInfo: filepath.Join(dir, "missing")}).dvARMv6() {
		t.Fatal("missing file is not v6")
	}
}

func TestDVUnexpectedDepsGuard(t *testing.T) {
	out := "Inst md380-emu (1.0 aslcp [armhf])\nInst qemu-user-static (1:8 Debian [arm64])\nInst libc6 [2.36] (2.36-9 Debian [arm64])\n"
	inst := ParseInstLines(out)
	if len(inst) != 3 || inst[0].Name != "md380-emu" || !inst[0].New || inst[2].New {
		t.Fatalf("%+v", inst)
	}
	if bad := UnexpectedDeps(inst); len(bad) != 0 {
		t.Fatalf("flagged %v", bad)
	}
	inst = append(inst, ParseInstLines("Inst asterisk (20.1 Debian [arm64])\n")...)
	for _, p := range dvForbiddenDepPrefixes {
		inst = append(inst, DVInst{Name: p + "x", New: true})
	}
	if bad := UnexpectedDeps(inst); len(bad) < len(dvForbiddenDepPrefixes) {
		t.Fatalf("guard missed packages: %v", bad)
	}
}

func TestDVEnableScript(t *testing.T) {
	o := &Ops{SelfPath: "/usr/bin/aslcp-helper", DVStateDir: "/var/lib/test-state"}
	s := o.dvEnableScript("arm64")
	order := []string{
		"apt-get update", "apt-get -s -q install", "dv-step " + StepInstallPlan, "apt-get install -y",
		"systemctl cat", "disable --now", "IPAddressDeny", "dv-step " + StepConfig, "daemon-reload",
		"enable --now " + dvMD380Unit + ".service " + dvEngineUnit + ".service", "dv-step " + StepCheck, "dv-step " + StepPurgeLegacy,
	}
	at := -1
	for _, w := range order {
		i := strings.Index(s[at+1:], w)
		if i < 0 {
			t.Fatalf("%q missing or out of order in:\n%s", w, s)
		}
		at += i + 1
	}
	if !strings.Contains(s, dvQemuPkg) {
		t.Error("arm64 needs qemu-user-static")
	}
	if strings.Contains(s, "apt-get purge") || strings.Contains(s, "apt-get remove") {
		t.Error("the enable job must not call apt to remove packages itself")
	}
	if strings.Contains(o.dvEnableScript("armhf"), dvQemuPkg) {
		t.Error("armhf needs no qemu")
	}
}

func TestDVUSRPPorts(t *testing.T) {
	rpt := []byte(dvBkRpt)
	if l, p, ok := dvUSRPPorts(rpt, "1998"); !ok || l != 34001 || p != 32001 {
		t.Fatalf("%d %d %v", l, p, ok)
	}
	for _, node := range []string{"1999", "1997", "abc"} {
		if _, _, ok := dvUSRPPorts(rpt, node); ok {
			t.Errorf("node %s accepted", node)
		}
	}
	bad := "[1998]\nrxchannel = USRP/192.0.2.5:34001:32001\n"
	if _, _, ok := dvUSRPPorts([]byte(bad), "1998"); ok {
		t.Error("non-loopback accepted")
	}
}

func dvCfgOps(t *testing.T) *Ops {
	t.Helper()
	u, err := user.Current()
	if err != nil {
		t.Skip(err)
	}
	e := newCfg(t)
	e.o.DVStateDir = filepath.Join(t.TempDir(), "state")
	e.o.DVEngineDir = filepath.Join(t.TempDir(), "engine")
	e.o.DVEngineUser = u.Username
	return e.o
}

func TestDVConfigDefaultsAndWrite(t *testing.T) {
	o := dvCfgOps(t)
	res, err := o.DVConfig(context.Background())
	if err != nil || !res.Changed {
		t.Fatalf("%+v %v", res, err)
	}
	var cfg dvctl.Config
	raw, _ := os.ReadFile(res.Path)
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.USRP.Listen != "127.0.0.1:34001" || cfg.USRP.Peer != "127.0.0.1:32001" || cfg.Vocoder.MD380 != "127.0.0.1:2470" {
		t.Fatalf("%+v", cfg)
	}
	if len(cfg.Networks) != 7 || cfg.Networks[0].Mode != dvctl.ModeLoop || !cfg.Networks[0].Enabled {
		t.Fatalf("%+v", cfg.Networks)
	}
	for i, m := range []string{dvctl.ModeBM, dvctl.ModeTGIF, dvctl.ModeDMR, dvctl.ModeYSF, dvctl.ModeP25, dvctl.ModeNXDN} {
		if n := cfg.Networks[i+1]; n.Mode != m || n.Enabled {
			t.Fatalf("%+v", cfg.Networks)
		}
	}
	if fi, _ := os.Stat(res.Path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
	if res2, err := o.DVConfig(context.Background()); err != nil || res2.Changed {
		t.Fatalf("second render must not change: %+v %v", res2, err)
	}
}

func TestDVConfigUsesPrivateNodePortsAndNetworks(t *testing.T) {
	o := dvCfgOps(t)
	if err := os.MkdirAll(o.ConfigRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(o.ConfigRoot, "rpt.conf"), []byte(strings.Replace(dvBkRpt, "34001:32001", "34011:32011", 1)), 0o644)
	rec := dvBkRecord("1998")
	if _, err := o.DVPrivate(context.Background(), &DVRequest{Private: &rec}); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(o.dvStateDir(), 0o700)
	os.WriteFile(o.dvNetworksPath(), []byte(dvBkNets), 0o600)
	res, err := o.DVConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var cfg dvctl.Config
	raw, _ := os.ReadFile(res.Path)
	json.Unmarshal(raw, &cfg)
	if cfg.USRP.Listen != "127.0.0.1:34011" || cfg.USRP.Peer != "127.0.0.1:32011" {
		t.Fatalf("%+v", cfg.USRP)
	}
	modes := map[string]dvctl.NetConfig{}
	for _, n := range cfg.Networks {
		modes[n.Mode] = n
	}
	if modes[dvctl.ModeBM].Enabled /* switch carried from 1.x, no master set up yet: off in the engine */ || modes[dvctl.ModeDMR].Address != "192.0.2.10" || modes[dvctl.ModeTGIF].Password != "example-pass" {
		t.Fatalf("%+v", cfg.Networks)
	}
}

func TestIsDVPackage(t *testing.T) {
	for _, n := range []string{"md380-emu", "qemu-user-static", "analog-bridge", "stfu"} {
		if !IsDVPackage(n) {
			t.Errorf("%s should be ours", n)
		}
	}
	for _, n := range []string{"wget", "asterisk", "curl"} {
		if IsDVPackage(n) {
			t.Errorf("%s should not be ours", n)
		}
	}
}

func TestMigrateDVState(t *testing.T) {
	root := t.TempDir()
	o := &Ops{
		DVStateDir: filepath.Join(root, "new-state"), DVOldStateDir: filepath.Join(root, "old-state"),
	}
	// nothing to do on a fresh node
	if moved, err := o.MigrateDVState(); err != nil || len(moved) != 0 {
		t.Fatalf("fresh: %v %v", moved, err)
	}
	for _, d := range []string{o.DVOldStateDir} {
		if err := os.MkdirAll(filepath.Join(d, "sub"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "sub", "f"), []byte(d), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	moved, err := o.MigrateDVState()
	if err != nil || len(moved) != 1 {
		t.Fatalf("moved %v %v", moved, err)
	}
	for _, p := range [][2]string{{o.DVOldStateDir, o.DVStateDir}} {
		if _, err := os.Stat(p[0]); !os.IsNotExist(err) {
			t.Errorf("old dir %s still there", p[0])
		}
		b, err := os.ReadFile(filepath.Join(p[1], "sub", "f"))
		if err != nil || string(b) != p[0] {
			t.Errorf("content of %s: %q %v", p[1], b, err)
		}
	}
	// idempotent
	if moved, err := o.MigrateDVState(); err != nil || len(moved) != 0 {
		t.Fatalf("again: %v %v", moved, err)
	}
	// an existing new dir is never overwritten and the old one is left alone
	if err := os.MkdirAll(o.DVOldStateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if moved, err := o.MigrateDVState(); err != nil || len(moved) != 0 {
		t.Fatalf("both: %v %v", moved, err)
	}
	if _, err := os.Stat(o.DVOldStateDir); err != nil {
		t.Error("old dir removed although the new one exists")
	}
}
