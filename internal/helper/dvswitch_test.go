package helper

import (
	"bytes"
	"context"
	"crypto/sha1" //nolint:gosec
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"
)

// ---- helpers

type funcRunner struct {
	fn    func(cmd string) (string, int, error)
	calls []string
}

func (f *funcRunner) Run(_ context.Context, argv ...string) (string, int, error) {
	cmd := strings.Join(argv, " ")
	f.calls = append(f.calls, cmd)
	return f.fn(cmd)
}

// synthKey builds a binary v4 public-key packet (tag 6, new format) with a
// recognizable body, and its fingerprint computed independently.
func synthKey(seed byte) ([]byte, string) {
	body := []byte{4, 0, 0, 0, 1, 1, 0, 16}
	for i := 0; i < 2; i++ {
		body = append(body, seed+byte(i), 0x55)
	}
	pkt := append([]byte{0xC0 | 6, byte(len(body))}, body...)
	h := sha1.New() //nolint:gosec
	h.Write([]byte{0x99, 0, byte(len(body))})
	h.Write(body)
	return pkt, strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

func armor(bin []byte, crc bool) []byte {
	var b bytes.Buffer
	b.WriteString(armorHeader + "\n\n")
	enc := base64.StdEncoding.EncodeToString(bin)
	for len(enc) > 64 {
		b.WriteString(enc[:64] + "\n")
		enc = enc[64:]
	}
	b.WriteString(enc + "\n")
	if crc {
		c := crc24(bin)
		b.WriteString("=" + base64.StdEncoding.EncodeToString([]byte{byte(c >> 16), byte(c >> 8), byte(c)}) + "\n")
	}
	b.WriteString("-----END PGP PUBLIC KEY BLOCK-----\n")
	return b.Bytes()
}

func pinKey(t *testing.T, fp string) {
	t.Helper()
	old := dvPinnedFP
	dvPinnedFP = fp
	t.Cleanup(func() { dvPinnedFP = old })
}

func dvOps(t *testing.T, run Runner) *Ops {
	t.Helper()
	d := t.TempDir()
	osr := filepath.Join(d, "os-release")
	os.WriteFile(osr, []byte("VERSION_CODENAME=bookworm\n"), 0o644)
	return &Ops{
		Run: run, OSRelease: osr,
		DVKeyring: filepath.Join(d, "keyrings", "dvswitch-keyring.gpg"), DVSourceList: filepath.Join(d, "sources", "dvswitch.list"),
		DVStateDir: filepath.Join(d, "state"), DVHostDir: filepath.Join(d, "mmdvm"),
		SelfPath: "/usr/libexec/aslcp/aslcp-helper",
		Now:      func() time.Time { return time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC) },
	}
}

// dvRules is a fakeRunner rule set for status: nothing installed, idle jobs.
func dvRules(installed ...string) map[string]resp {
	var sb strings.Builder
	for _, p := range installed {
		fmt.Fprintf(&sb, "%s\tinstalled\t1.2.3\n", p)
	}
	r := map[string]resp{
		"dpkg-query":                {out: sb.String()},
		"dpkg --print-architecture": {out: "amd64\n"},
		"systemctl show -p Id":      {out: "Id=analog_bridge.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\n"},
	}
	idleJob(r)
	return r
}

// ---- constants and scripts

func TestDVPackageListConstant(t *testing.T) {
	want := []string{"dvswitch-base", "analog-bridge", "mmdvm-bridge", "md380-emu", "ysfgateway", "p25gateway", "nxdngateway", "stfu", "wget"}
	if got := DVSwitchPackages(); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("packages: %v", got)
	}
	o := dvOps(t, &fakeRunner{})
	if !strings.Contains(o.dvInstallScript(), "install --no-install-recommends "+dvInstallPackages) {
		t.Error("install script lacks the package constant")
	}
	if !strings.Contains(o.dvInstallScript(), "systemctl disable --now stfu.service || true") {
		t.Error("install script leaves stfu running")
	}
	if strings.Contains(o.dvInstallScript(), "qemu") {
		t.Error("qemu-user-static must come from the simulation, not the script")
	}
}

func TestDVUninstallPathsFixed(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	paths := o.DVSwitchUninstallPaths()
	for _, w := range []string{"/var/lib/mmdvm", "/var/lib/dvswitch", "/var/log/dvswitch", "/var/log/mmdvm", "/opt/Analog_Bridge", "/opt/MMDVM_Bridge",
		"/etc/cron.d/dvswitch", "/etc/cron.daily/dvswitch-log-cleanup", "/tmp/ABInfo_*.json", o.DVSourceList, o.DVKeyring} {
		found := false
		for _, p := range paths {
			found = found || p == w
		}
		if !found {
			t.Errorf("missing path %s", w)
		}
	}
	s := o.dvUninstallScript()
	for _, w := range paths {
		if !strings.Contains(s, w) {
			t.Errorf("script does not remove %s", w)
		}
	}
	// marker removed last (before the final echo only), no autoremove
	if strings.Contains(s, "autoremove") {
		t.Error("global autoremove in the script")
	}
	lines := strings.Split(s, "\n")
	if !strings.Contains(lines[len(lines)-2], o.dvMarkerPath()) {
		t.Errorf("marker not removed last: %q", lines[len(lines)-2])
	}
	if strings.Contains(s, "TODO(M2)") {
		t.Error("the M2 placeholder is still in the script")
	}
	rev, purge, marker := strings.Index(s, "dvswitch-step "+StepPrivateRev), strings.Index(s, "apt-get purge"), strings.Index(s, "rm -f -- "+shQuote(o.dvMarkerPath()))
	if rev < 0 || rev < purge || rev > marker {
		t.Errorf("private-node revert must run after the purge and before the marker goes (%d %d %d)", purge, rev, marker)
	}
	if !strings.Contains(s, "rm -rf -- "+shQuote(o.dvHistoryDir())) {
		t.Error("history dir not removed")
	}
	if !strings.HasSuffix(strings.Split(s, "\n")[1], "dvswitch-step uninstall-plan") {
		t.Error("plan step must run before anything is changed")
	}
	if strings.Index(s, "apt-get purge") > strings.Index(s, "apt-get update") {
		t.Error("update must follow the purge")
	}
	for _, u := range dvUnits {
		if !strings.Contains(s, "systemctl disable --now "+u+".service") {
			t.Errorf("unit %s not stopped", u)
		}
	}
}

func TestDVScriptsSyntax(t *testing.T) {
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	o := dvOps(t, &fakeRunner{})
	for name, s := range map[string]string{"install": o.dvInstallScript(), "uninstall": o.dvUninstallScript()} {
		if out, err := exec.Command(sh, "-n", "-c", s).CombinedOutput(); err != nil {
			t.Errorf("%s script: %v %s", name, err, out)
		}
	}
	argv := o.dvUninstallArgv()
	if argv[0] != "systemd-run" || argv[3] != "--collect" || !strings.Contains(strings.Join(argv, " "), "--unit="+dvUninstallUnit) {
		t.Errorf("argv: %v", argv)
	}
}

// ---- parsing

const dvSim = `NOTE: This is only a simulation!
Inst qemu-user-static (1:7.2 Debian:12/stable [amd64])
Inst wget (1.21 Debian:12/stable [amd64])
Inst libc6 [2.36-9] (2.36-9+deb12u1 Debian:12 [amd64])
Inst dvswitch-base (1.7 dvswitch [all])
Inst analog-bridge (1.6.0 dvswitch [amd64])
Conf dvswitch-base (1.7 dvswitch [all])
`

func TestParseInstLines(t *testing.T) {
	got := ParseInstLines(dvSim)
	want := []DVInst{{"qemu-user-static", true}, {"wget", true}, {"libc6", false}, {"dvswitch-base", true}, {"analog-bridge", true}}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if len(ParseInstLines("Conf x (1)\nRemv y [1]\n")) != 0 {
		t.Error("non-Inst lines parsed")
	}
}

func TestParseRemovals(t *testing.T) {
	got := ParseRemovals("Remv b [1]\nPurg b [1]\nPurg a [2]\nInst c (3)\n")
	if strings.Join(got, ",") != "a,b" {
		t.Fatalf("got %v", got)
	}
}

func TestUnexpectedDeps(t *testing.T) {
	inst := ParseInstLines("Inst apache2 (2)\nInst php8.2-cli (1)\nInst mosquitto (2)\nInst nodejs (18)\nInst qemu-user-static (1)\nInst libapache2-mod-php8.2 (1)\n")
	got := UnexpectedDeps(inst)
	if len(got) != 5 {
		t.Fatalf("got %v", got)
	}
	if len(UnexpectedDeps(ParseInstLines(dvSim))) != 0 {
		t.Error("the normal plan was flagged")
	}
}

// ---- fingerprint

func TestKeyFingerprint(t *testing.T) {
	bin, fp := synthKey(1)
	got, err := KeyringFingerprints(bin)
	if err != nil || len(got) != 1 || got[0] != fp {
		t.Fatalf("binary: %v %v", got, err)
	}
	got, err = KeyringFingerprints(armor(bin, true))
	if err != nil || got[0] != fp {
		t.Fatalf("armored: %v %v", got, err)
	}
	// old-format packet header with the same body gives the same fingerprint
	body := bin[2:]
	old := append([]byte{0x80 | 6<<2, byte(len(body))}, body...)
	if got, err := KeyringFingerprints(old); err != nil || got[0] != fp {
		t.Fatalf("old format: %v %v", got, err)
	}
}

func TestKeyFingerprintAgainstGPG(t *testing.T) {
	gpg, err := exec.LookPath("gpg")
	if err != nil {
		t.Skip("no gpg")
	}
	home := t.TempDir()
	run := func(args ...string) []byte {
		cmd := exec.Command(gpg, append([]string{"--homedir", home, "--batch", "--yes", "--pinentry-mode", "loopback"}, args...)...)
		out, err := cmd.Output()
		if err != nil {
			t.Skipf("gpg unusable: %v", err)
		}
		return out
	}
	run("--passphrase", "", "--quick-generate-key", "N0CALL Test <n0call@example.invalid>", "rsa2048", "sign", "never")
	pub := run("--export")
	colons := string(run("--list-keys", "--with-colons", "--fingerprint"))
	var want string
	for _, l := range strings.Split(colons, "\n") {
		if strings.HasPrefix(l, "fpr:") {
			want = strings.Split(l, ":")[9]
			break
		}
	}
	got, err := KeyringFingerprints(pub)
	if err != nil || len(got) != 1 || got[0] != want {
		t.Fatalf("got %v %v want %s", got, err, want)
	}
	if _, err := VerifyDVKeyring(pub); !errors.Is(err, ErrDVKeyMismatch) {
		t.Fatalf("a foreign key must not verify: %v", err)
	}
	pinKey(t, want)
	if bin, err := VerifyDVKeyring(armor(pub, true)); err != nil || !bytes.Equal(bin, pub) {
		t.Fatalf("pinned armored: %v", err)
	}
}

func TestVerifyDVKeyring(t *testing.T) {
	bin, fp := synthKey(1)
	other, _ := synthKey(9)
	if _, err := VerifyDVKeyring(bin); !errors.Is(err, ErrDVKeyMismatch) {
		t.Fatalf("unpinned key accepted: %v", err)
	}
	pinKey(t, fp)
	out, err := VerifyDVKeyring(armor(bin, true))
	if err != nil || !bytes.Equal(out, bin) {
		t.Fatalf("pinned: %v", err)
	}
	if _, err := VerifyDVKeyring(append(append([]byte{}, bin...), other...)); !errors.Is(err, ErrDVKeyMismatch) {
		t.Errorf("two primary keys accepted: %v", err)
	}
	if _, err := VerifyDVKeyring(bin[:len(bin)-2]); !errors.Is(err, ErrDVKeyMismatch) {
		t.Errorf("truncated: %v", err)
	}
	bad := armor(bin, true)
	bad = bytes.Replace(bad, []byte("\n="), []byte("\n=AAAA"), 1)
	if _, err := VerifyDVKeyring(bad); !errors.Is(err, ErrDVKeyMismatch) {
		t.Errorf("bad crc: %v", err)
	}
	for name, d := range map[string][]byte{
		"empty": {}, "html": []byte("<html>404</html>"),
		"partial": {0xC0 | 6, 0xE1, 1, 2, 3},
		"secret":  {0xC0 | 5, 3, 4, 0, 0},
		"v3":      {0xC0 | 6, 6, 3, 0, 0, 0, 1, 1},
		"indet":   {0x80 | 6<<2 | 3, 1, 2},
	} {
		if _, err := VerifyDVKeyring(d); !errors.Is(err, ErrDVKeyMismatch) {
			t.Errorf("%s accepted: %v", name, err)
		}
	}
}

// ---- marker

func TestMarkerReadWrite(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state", "install.json")
	if m, err := ReadDVMarker(p); m != nil || err != nil {
		t.Fatalf("absent: %v %v", m, err)
	}
	in := DVSwitchMarker{Version: 1, InstalledAt: "2026-11-17T12:00:00Z", WgetPreinstalled: true, Packages: []string{"qemu-user-static", "analog-bridge"}}
	if err := WriteDVMarker(p, in); err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(p)
	di, _ := os.Stat(filepath.Dir(p))
	if fi.Mode().Perm() != 0o600 || di.Mode().Perm() != 0o700 {
		t.Errorf("modes %v %v", fi.Mode().Perm(), di.Mode().Perm())
	}
	out, err := ReadDVMarker(p)
	if err != nil || out == nil || !out.WgetPreinstalled || strings.Join(out.Packages, ",") != "analog-bridge,qemu-user-static" {
		t.Fatalf("read back: %+v %v", out, err)
	}
	for name, content := range map[string]string{
		"garbage": "nope", "version": `{"version":2,"packages":[]}`, "unknown field": `{"version":1,"x":1,"packages":[]}`,
		"bad name": `{"version":1,"packages":["a b"]}`, "flag name": `{"version":1,"packages":["-rf"]}`,
	} {
		os.WriteFile(p, []byte(content), 0o600)
		if m, err := ReadDVMarker(p); err == nil || m != nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestMergeDVMarker(t *testing.T) {
	t0 := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	m := MergeDVMarker(nil, []string{"b", "a", "wget"}, true, t0)
	if !m.WgetPreinstalled || strings.Join(m.Packages, ",") != "a,b" {
		t.Errorf("wget before: %+v", m)
	}
	m = MergeDVMarker(nil, []string{"b", "wget"}, false, t0)
	if m.WgetPreinstalled || strings.Join(m.Packages, ",") != "b,wget" || m.InstalledAt != "2026-01-02T03:04:05Z" {
		t.Errorf("fresh: %+v", m)
	}
	// a second run (wget now installed, so its simulation says wget was there)
	m2 := MergeDVMarker(&m, []string{"c"}, true, t0.Add(time.Hour))
	if m2.WgetPreinstalled || strings.Join(m2.Packages, ",") != "b,c,wget" || m2.InstalledAt != m.InstalledAt {
		t.Errorf("merge keeps the first facts: %+v", m2)
	}
	if got := MergeDVMarker(nil, nil, false, t0).Packages; got == nil {
		t.Error("nil packages would marshal to null")
	}
}

// ---- install-plan step

func TestInstallPlanRecordsMarker(t *testing.T) {
	r := dvRules()
	o := dvOps(t, &fakeRunner{rules: r}) // wget not installed
	os.MkdirAll(o.DVStateDir, 0o700)
	os.WriteFile(o.dvSimPath(), []byte(dvSim), 0o600)
	var w bytes.Buffer
	if err := o.RunDVSwitchStep(context.Background(), StepInstallPlan, &w); err != nil {
		t.Fatal(err, w.String())
	}
	m, err := ReadDVMarker(o.dvMarkerPath())
	if err != nil || m == nil {
		t.Fatal(err)
	}
	if strings.Join(m.Packages, ",") != "analog-bridge,dvswitch-base,qemu-user-static,wget" || m.WgetPreinstalled || m.InstalledAt != "2026-11-17T12:00:00Z" {
		t.Errorf("%+v", m)
	}
	if _, err := os.Stat(o.dvSimPath()); err == nil {
		t.Error("sim file left behind")
	}
}

func TestInstallPlanWgetPreinstalled(t *testing.T) {
	o := dvOps(t, &fakeRunner{rules: dvRules("wget")})
	os.MkdirAll(o.DVStateDir, 0o700)
	os.WriteFile(o.dvSimPath(), []byte("Inst qemu-user-static (1 x [amd64])\nInst wget [1.0] (1.1 x [amd64])\n"), 0o600)
	if err := o.RunDVSwitchStep(context.Background(), StepInstallPlan, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	m, _ := ReadDVMarker(o.dvMarkerPath())
	if !m.WgetPreinstalled || strings.Join(m.Packages, ",") != "qemu-user-static" {
		t.Errorf("%+v", m)
	}
}

func TestInstallPlanUnexpectedDeps(t *testing.T) {
	o := dvOps(t, &fakeRunner{rules: dvRules()})
	os.MkdirAll(o.DVStateDir, 0o700)
	os.MkdirAll(filepath.Dir(o.DVSourceList), 0o755)
	os.MkdirAll(filepath.Dir(o.DVKeyring), 0o755)
	os.WriteFile(o.DVSourceList, []byte("x"), 0o644)
	os.WriteFile(o.DVKeyring, []byte("x"), 0o644)
	os.WriteFile(o.dvSimPath(), []byte("Inst apache2 (2 x)\nInst dvswitch-base (1 x)\n"), 0o600)
	var w bytes.Buffer
	err := o.RunDVSwitchStep(context.Background(), StepInstallPlan, &w)
	if err == nil || err.Error() != "unexpected_deps" || !strings.Contains(w.String(), "apache2") {
		t.Fatalf("%v %q", err, w.String())
	}
	if m, _ := ReadDVMarker(o.dvMarkerPath()); m != nil {
		t.Error("marker written for a refused plan")
	}
	if _, err := os.Stat(o.DVSourceList); err == nil {
		t.Error("source left behind")
	}
	if _, err := os.Stat(o.DVKeyring); err == nil {
		t.Error("key left behind")
	}
}

func TestInstallCheckWarns(t *testing.T) {
	o := dvOps(t, &fakeRunner{})
	os.MkdirAll(o.DVHostDir, 0o755)
	os.WriteFile(filepath.Join(o.DVHostDir, "YSFHosts.txt"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(o.DVHostDir, "DMRIds.dat"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(o.DVHostDir, "P25Hosts.txt"), nil, 0o644)
	var w bytes.Buffer
	if err := o.RunDVSwitchStep(context.Background(), StepInstallCheck, &w); err != nil {
		t.Fatal(err)
	}
	if strings.Count(w.String(), "warning:") != 2 {
		t.Errorf("%q", w.String())
	}
	if err := o.RunDVSwitchStep(context.Background(), "bogus", &w); err == nil {
		t.Error("unknown step accepted")
	}
}

// ---- uninstall-plan step

const simPrefix = "env LC_ALL=C DEBIAN_FRONTEND=noninteractive apt-get -s -q -o Debug::NoLocking=1 purge "

// purgeSim fakes apt: each name in extra pulls the listed packages into the
// removal of any simulation that contains it.
func purgeRunner(installed []string, pulls map[string][]string) *funcRunner {
	return &funcRunner{fn: func(cmd string) (string, int, error) {
		switch {
		case strings.HasPrefix(cmd, "dpkg-query"):
			var sb strings.Builder
			for _, p := range installed {
				fmt.Fprintf(&sb, "%s\tinstalled\t1\n", p)
			}
			return sb.String(), 0, nil
		case strings.HasPrefix(cmd, simPrefix):
			names := strings.Fields(strings.TrimPrefix(cmd, simPrefix))
			set := map[string]bool{}
			for _, n := range names {
				set[n] = true
				for _, x := range pulls[n] {
					set[x] = true
				}
			}
			var keys []string
			for k := range set {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			var sb strings.Builder
			for _, k := range keys {
				fmt.Fprintf(&sb, "Purg %s [1]\n", k)
			}
			return sb.String(), 0, nil
		}
		return "", 1, errors.New("unexpected " + cmd)
	}}
}

func writeMarker(t *testing.T, o *Ops, wget bool, pk ...string) {
	t.Helper()
	if err := WriteDVMarker(o.dvMarkerPath(), DVSwitchMarker{Version: 1, InstalledAt: "2026-11-17T12:00:00Z", WgetPreinstalled: wget, Packages: pk}); err != nil {
		t.Fatal(err)
	}
}

func readPurge(t *testing.T, o *Ops) []string {
	t.Helper()
	b, err := os.ReadFile(o.dvPurgePath())
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(string(b))
}

func TestUninstallPlanPurgesSetAndDeps(t *testing.T) {
	inst := append(DVSwitchPackages(), "qemu-user-static")
	o := dvOps(t, purgeRunner(inst, nil))
	writeMarker(t, o, false, "qemu-user-static", "wget", "analog-bridge")
	var w bytes.Buffer
	if err := o.RunDVSwitchStep(context.Background(), StepUninstallPlan, &w); err != nil {
		t.Fatal(err, w.String())
	}
	got := readPurge(t, o)
	if len(got) != 10 || !containsStr(got, "stfu") || !containsStr(got, "qemu-user-static") || !containsStr(got, "wget") {
		t.Errorf("%v", got)
	}
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func TestUninstallPlanSkipsPreinstalledWget(t *testing.T) {
	o := dvOps(t, purgeRunner(append(DVSwitchPackages(), "qemu-user-static"), nil))
	writeMarker(t, o, true, "qemu-user-static")
	var w bytes.Buffer
	if err := o.RunDVSwitchStep(context.Background(), StepUninstallPlan, &w); err != nil {
		t.Fatal(err)
	}
	if got := readPurge(t, o); containsStr(got, "wget") || !strings.Contains(w.String(), "keeping wget") {
		t.Errorf("%v %q", got, w.String())
	}
	// even a marker that lists wget does not make it a candidate
	writeMarker(t, o, true, "wget")
	if c := dvPurgeCandidates(&DVSwitchMarker{WgetPreinstalled: true, Packages: []string{"wget"}}); containsStr(c, "wget") {
		t.Error("wget candidate")
	}
}

func TestUninstallPlanKeepsNeededDep(t *testing.T) {
	// removing libfoo would also remove otherapp, which is not recorded
	r := purgeRunner(append(DVSwitchPackages(), "libfoo", "qemu-user-static"), map[string][]string{"libfoo": {"otherapp"}})
	o := dvOps(t, r)
	writeMarker(t, o, false, "libfoo", "qemu-user-static")
	var w bytes.Buffer
	if err := o.RunDVSwitchStep(context.Background(), StepUninstallPlan, &w); err != nil {
		t.Fatal(err, w.String())
	}
	got := readPurge(t, o)
	if containsStr(got, "libfoo") || !containsStr(got, "qemu-user-static") || !strings.Contains(w.String(), "keeping libfoo") {
		t.Errorf("%v %q", got, w.String())
	}
}

func TestUninstallPlanUnexpectedRemovals(t *testing.T) {
	for name, pull := range map[string]string{"foreign": "otherapp", "asterisk": "asterisk", "allstarlink": "allstarlink-foo", "asl3": "asl3"} {
		o := dvOps(t, purgeRunner(DVSwitchPackages(), map[string][]string{"analog-bridge": {pull}}))
		writeMarker(t, o, false)
		var w bytes.Buffer
		err := o.RunDVSwitchStep(context.Background(), StepUninstallPlan, &w)
		if err == nil || err.Error() != "unexpected_removals" || !strings.Contains(w.String(), "unexpected_removals") || !strings.Contains(w.String(), pull) {
			t.Errorf("%s: %v %q", name, err, w.String())
		}
		if _, err := os.Stat(o.dvPurgePath()); err == nil {
			t.Errorf("%s: purge list written for an aborted plan", name)
		}
	}
}

func TestUninstallPlanNoMarker(t *testing.T) {
	o := dvOps(t, purgeRunner(DVSwitchPackages(), nil))
	if err := o.RunDVSwitchStep(context.Background(), StepUninstallPlan, &bytes.Buffer{}); !errors.Is(err, ErrDVOutsideASLCP) {
		t.Fatal(err)
	}
}

func TestUninstallPlanNothingInstalled(t *testing.T) {
	o := dvOps(t, purgeRunner(nil, nil))
	writeMarker(t, o, false, "qemu-user-static")
	var w bytes.Buffer
	if err := o.RunDVSwitchStep(context.Background(), StepUninstallPlan, &w); err != nil {
		t.Fatal(err)
	}
	if got := readPurge(t, o); len(got) != 0 {
		t.Errorf("%v", got)
	}
}

// ---- ops: status, install, uninstall

func TestDVSwitchStatusFresh(t *testing.T) {
	o := dvOps(t, &fakeRunner{rules: dvRules()})
	st, err := o.DVSwitchStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Installed || st.InstalledBy != "" || !st.Supported || !st.CanInstall || st.CanUninstall || st.Arch != "amd64" || st.OSCodename != "bookworm" {
		t.Errorf("%+v", st)
	}
	if len(st.Units) != len(dvUnits) || len(st.Packages) != len(DVSwitchPackages()) || len(st.UninstallPaths) == 0 {
		t.Errorf("%+v", st)
	}
}

func TestDVSwitchStatusExternalAndASLCP(t *testing.T) {
	o := dvOps(t, &fakeRunner{rules: dvRules("dvswitch-base", "analog-bridge", "wget")})
	st, _ := o.DVSwitchStatus(context.Background())
	if !st.Installed || st.InstalledBy != "external" || st.CanInstall || st.CanUninstall || st.Version != "1.2.3" {
		t.Errorf("external: %+v", st)
	}
	server := dvOps(t, &fakeRunner{rules: dvRules("dvswitch-server")})
	writeMarker(t, server, false)
	if st, _ := server.DVSwitchStatus(context.Background()); st.CanInstall || !st.CanUninstall || !st.Installed {
		t.Errorf("server: %+v", st)
	}
	writeMarker(t, o, false, "qemu-user-static")
	st, _ = o.DVSwitchStatus(context.Background())
	if st.InstalledBy != "aslcp" || !st.CanUninstall || !st.CanInstall /* incomplete set: resume */ {
		t.Errorf("aslcp: %+v", st)
	}
	// corrupt marker: not trusted, no uninstall, no install over it
	os.WriteFile(o.dvMarkerPath(), []byte("x"), 0o600)
	if st, _ := o.DVSwitchStatus(context.Background()); st.CanUninstall || st.CanInstall {
		t.Errorf("corrupt: %+v", st)
	}
}

func TestDVSwitchStatusKeyAndRepo(t *testing.T) {
	bin, fp := synthKey(3)
	o := dvOps(t, &fakeRunner{rules: dvRules()})
	os.MkdirAll(filepath.Dir(o.DVKeyring), 0o755)
	os.MkdirAll(filepath.Dir(o.DVSourceList), 0o755)
	os.WriteFile(o.DVKeyring, bin, 0o644)
	os.WriteFile(o.DVSourceList, []byte(dvSourceLine(o.DVKeyring)), 0o644)
	st, _ := o.DVSwitchStatus(context.Background())
	if !st.KeyPresent || st.KeyOK || st.KeyFingerprint != fp || !st.RepoPresent {
		t.Errorf("%+v", st)
	}
	pinKey(t, fp)
	if st, _ := o.DVSwitchStatus(context.Background()); !st.KeyOK {
		t.Error("key not ok")
	}
}

func TestDVPlatform(t *testing.T) {
	for _, c := range []struct {
		cn, arch string
		ok       bool
	}{{"bookworm", "amd64", true}, {"trixie", "arm64", true}, {"bookworm", "armhf", true}, {"bullseye", "amd64", false}, {"bookworm", "i386", false}, {"", "amd64", false}} {
		if ok, why := DVPlatform(c.cn, c.arch); ok != c.ok || !ok && why == "" {
			t.Errorf("%v: %v %q", c, ok, why)
		}
	}
}

func goodGet(bin []byte) func(context.Context, string) ([]byte, error) {
	return func(_ context.Context, url string) ([]byte, error) {
		if url != DVKeyURL {
			return nil, errors.New("unexpected url " + url)
		}
		return bin, nil
	}
}

func startRule(r map[string]resp) { r["systemd-run"] = resp{} }

func TestDVSwitchInstall(t *testing.T) {
	bin, fp := synthKey(1)
	pinKey(t, fp)
	r := dvRules()
	startRule(r)
	f := &fakeRunner{rules: r}
	o := dvOps(t, f)
	o.HTTPGet = goodGet(armor(bin, true))
	st, err := o.DVSwitchInstall(context.Background())
	if err != nil || st.Install.Job != JobRunning || st.CanInstall {
		t.Fatalf("%+v %v", st, err)
	}
	if f.called("systemd-run") != 1 {
		t.Error("unit not started once")
	}
	if got, _ := os.ReadFile(o.DVKeyring); !bytes.Equal(got, bin) {
		t.Error("keyring is not the verified binary key")
	}
	src, _ := os.ReadFile(o.DVSourceList)
	if !strings.Contains(string(src), "signed-by="+o.DVKeyring) || !strings.Contains(string(src), "bookworm") {
		t.Errorf("source: %q", src)
	}
	for _, p := range []string{o.DVKeyring, o.DVSourceList} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o644 {
			t.Errorf("%s: %v %v", p, fi, err)
		}
	}
}

func TestDVSwitchInstallRefusals(t *testing.T) {
	bin, fp := synthKey(1)
	other, _ := synthKey(7)
	ctx := context.Background()
	untouched := func(o *Ops, f *fakeRunner) {
		t.Helper()
		if f.called("systemd-run") != 0 {
			t.Error("unit started")
		}
		for _, p := range []string{o.DVKeyring, o.DVSourceList} {
			if _, err := os.Stat(p); err == nil {
				t.Errorf("%s written", p)
			}
		}
	}
	t.Run("key mismatch", func(t *testing.T) {
		pinKey(t, fp)
		r := dvRules()
		startRule(r)
		f := &fakeRunner{rules: r}
		o := dvOps(t, f)
		o.HTTPGet = goodGet(other)
		if _, err := o.DVSwitchInstall(ctx); !errors.Is(err, ErrDVKeyMismatch) {
			t.Fatal(err)
		}
		untouched(o, f)
	})
	t.Run("download fails", func(t *testing.T) {
		f := &fakeRunner{rules: dvRules()}
		o := dvOps(t, f)
		o.HTTPGet = func(context.Context, string) ([]byte, error) { return nil, errors.New("boom") }
		if _, err := o.DVSwitchInstall(ctx); err == nil {
			t.Fatal("no error")
		}
		untouched(o, f)
	})
	t.Run("clock", func(t *testing.T) {
		f := &fakeRunner{rules: dvRules()}
		o := dvOps(t, f)
		o.Now = func() time.Time { return time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC) }
		o.HTTPGet = goodGet(bin)
		if _, err := o.DVSwitchInstall(ctx); !errors.Is(err, ErrDVClockUnset) {
			t.Fatal(err)
		}
		untouched(o, f)
	})
	t.Run("unsupported", func(t *testing.T) {
		f := &fakeRunner{rules: dvRules()}
		o := dvOps(t, f)
		os.WriteFile(o.OSRelease, []byte("VERSION_CODENAME=bullseye\n"), 0o644)
		o.HTTPGet = goodGet(bin)
		if _, err := o.DVSwitchInstall(ctx); !errors.Is(err, ErrDVUnsupported) {
			t.Fatal(err)
		}
		untouched(o, f)
	})
	t.Run("already installed", func(t *testing.T) {
		f := &fakeRunner{rules: dvRules("dvswitch-base", "analog-bridge")}
		o := dvOps(t, f)
		o.HTTPGet = goodGet(bin)
		if _, err := o.DVSwitchInstall(ctx); !errors.Is(err, ErrDVAlreadyInstalled) {
			t.Fatal(err)
		}
		untouched(o, f)
	})
	t.Run("busy", func(t *testing.T) {
		r := dvRules()
		r["systemctl list-units"] = resp{out: "aslcp-dvswitch-uninstall.service loaded active running x\n"}
		r["systemctl show aslcp-dvswitch-uninstall"] = resp{out: "ActiveState=active\nResult=success\n"}
		f := &fakeRunner{rules: r}
		o := dvOps(t, f)
		o.HTTPGet = goodGet(bin)
		if _, err := o.DVSwitchInstall(ctx); !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
		untouched(o, f)
	})
}

func TestDVSwitchUninstall(t *testing.T) {
	ctx := context.Background()
	t.Run("no marker", func(t *testing.T) {
		r := dvRules("dvswitch-base", "analog-bridge")
		startRule(r)
		f := &fakeRunner{rules: r}
		o := dvOps(t, f)
		if _, err := o.DVSwitchUninstall(ctx); !errors.Is(err, ErrDVOutsideASLCP) {
			t.Fatal(err)
		}
		if f.called("systemd-run") != 0 {
			t.Error("unit started")
		}
	})
	t.Run("marker", func(t *testing.T) {
		r := dvRules(DVSwitchPackages()...)
		startRule(r)
		f := &fakeRunner{rules: r}
		o := dvOps(t, f)
		writeMarker(t, o, false, "qemu-user-static")
		st, err := o.DVSwitchUninstall(ctx)
		if err != nil || st.Uninstall.Job != JobRunning {
			t.Fatalf("%+v %v", st, err)
		}
		if f.called("systemd-run --no-block --unit=aslcp-dvswitch-uninstall") != 1 {
			t.Errorf("calls: %v", f.calls)
		}
	})
	t.Run("busy", func(t *testing.T) {
		r := dvRules(DVSwitchPackages()...)
		r["systemctl list-units"] = resp{out: "aslcp-osupgrade.service loaded active running x\n"}
		r["systemctl show aslcp-osupgrade"] = resp{out: "ActiveState=active\nResult=success\n"}
		f := &fakeRunner{rules: r}
		o := dvOps(t, f)
		writeMarker(t, o, false)
		if _, err := o.DVSwitchUninstall(ctx); !errors.Is(err, ErrBusy) {
			t.Fatal(err)
		}
	})
}

func TestDVErrorsRoundTripPrefixes(t *testing.T) {
	for _, e := range dvErrors {
		if !strings.Contains(e.Error(), ":") {
			t.Errorf("%v has no code prefix", e)
		}
	}
}

func TestDemoDVSwitch(t *testing.T) {
	d := NewDemoDVSwitch()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	ctx := context.Background()
	if _, err := d.DVSwitchUninstall(ctx); !errors.Is(err, ErrDVOutsideASLCP) {
		t.Fatal(err)
	}
	if st, err := d.DVSwitchInstall(ctx); err != nil || st.Install.Job != JobRunning || st.CanInstall {
		t.Fatalf("%+v %v", st, err)
	}
	if _, err := d.DVSwitchInstall(ctx); !errors.Is(err, ErrBusy) {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	st, _ := d.DVSwitchStatus(ctx)
	if !st.Installed || !st.CanUninstall || st.Install.Job != JobSucceeded || st.InstalledBy != "aslcp" {
		t.Fatalf("%+v", st)
	}
	if _, err := d.DVSwitchInstall(ctx); !errors.Is(err, ErrDVAlreadyInstalled) {
		t.Fatal(err)
	}
	d.DVSwitchUninstall(ctx)
	now = now.Add(time.Minute)
	if st, _ := d.DVSwitchStatus(ctx); st.Installed || !st.CanInstall || st.Uninstall.Job != JobSucceeded {
		t.Fatalf("%+v", st)
	}
}

func TestDemoDVSwitchNXDNID(t *testing.T) {
	d := NewDemoDVSwitch()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	d.now = func() time.Time { return now }
	ctx := context.Background()
	d.DVSwitchInstall(ctx)
	now = now.Add(time.Minute)
	v, _ := d.DVSwitchSetup(ctx)
	if v.Identity.NXDNID == "" || len(noStock(v.Warnings)) != 0 {
		t.Fatalf("%+v %v", v.Identity, v.Warnings)
	}
	id := DVIdentity{Callsign: "N0CALL", DMRID: "1234567", ESSID: "01", NXDNID: "70000"}
	if _, err := d.DVSwitchIdentity(ctx, DVSwitchRequest{Identity: &id}); !errors.Is(err, ErrDVInvalid) {
		t.Fatal(err)
	}
	id.NXDNID = "23456"
	if _, err := d.DVSwitchIdentity(ctx, DVSwitchRequest{Identity: &id}); err != nil {
		t.Fatal(err)
	}
	id.NXDNID = ""
	d.DVSwitchIdentity(ctx, DVSwitchRequest{Identity: &id})
	if v, _ = d.DVSwitchSetup(ctx); v.Identity.NXDNID != "23456" {
		t.Fatalf("empty must keep the value: %+v", v.Identity)
	}
	on := true
	d.DVSwitchModes(ctx, DVSwitchRequest{Modes: &DVModes{NXDN: &on}})
	if v, _ = d.DVSwitchSetup(ctx); len(noStock(v.Warnings)) != 0 {
		t.Fatalf("%v", v.Warnings)
	}
}

func TestDVJobTime(t *testing.T) {
	f := &fakeRunner{rules: map[string]resp{
		"journalctl -u " + dvInstallUnit + " -n 1 -o short-unix":   {out: "1794950400.123456 node sh[1]: dvswitch install finished\n"},
		"journalctl -u " + dvUninstallUnit + " -n 1 -o short-unix": {out: "1794940000.5 node sh[1]: dvswitch uninstall finished\n"},
	}}
	o := dvOps(t, f)
	if got := o.dvJobTime(context.Background(), dvInstallUnit, JobSucceeded); got != 1794950400 {
		t.Errorf("install at = %d", got)
	}
	if got := o.dvJobTime(context.Background(), dvUninstallUnit, JobSucceeded); got != 1794940000 {
		t.Errorf("uninstall at = %d", got)
	}
	if got := o.dvJobTime(context.Background(), dvInstallUnit, JobIdle); got != 0 {
		t.Errorf("idle at = %d", got)
	}
}
