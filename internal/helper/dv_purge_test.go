package helper

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/artw1/aslcp/internal/legacy"
)

type purgeEnv struct {
	o    *Ops
	f    *fakeRunner
	root string
}

func newPurgeEnv(t *testing.T, markerPkgs []string, rules map[string]resp) *purgeEnv {
	t.Helper()
	root := t.TempDir()
	o := &Ops{
		DVStateDir:      filepath.Join(root, "state"),
		DVHistoryDir:    filepath.Join(root, "hist"),
		DVOldHistoryDir: filepath.Join(root, "oldhist"),
		DVKeyring:       filepath.Join(root, "keyring.gpg"),
		DVSourceList:    filepath.Join(root, "src.list"),
		DVPurgeSleep:    func(time.Duration) {},
		Now:             func() time.Time { return time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC) },
	}
	if markerPkgs != nil {
		if err := WriteDVMarker(o.dvMarkerPath(), DVMarker{Version: 1, InstalledAt: "2026-01-01T00:00:00Z", Packages: markerPkgs}); err != nil {
			t.Fatal(err)
		}
	}
	base := map[string]resp{
		"dpkg-query -W -f=${db:Status-Status} aslcp": {out: "installed"},
		"systemctl show":          {out: "Id=mmdvm_bridge.service\nLoadState=loaded\nActiveState=active\nUnitFileState=enabled\n\nId=stfu.service\nLoadState=not-found\nActiveState=inactive\nUnitFileState=\n"},
		"systemctl disable --now": {},
		"apt-mark manual":         {},
	}
	for k, v := range rules {
		base[k] = v
	}
	f := &fakeRunner{rules: base}
	o.Run = f
	return &purgeEnv{o: o, f: f, root: root}
}

const purgeDpkg = "dpkg-query -W -f=${Package}\t${db:Status-Status}\t${Version}\n "

func TestPurgeCandidates(t *testing.T) {
	for _, n := range []string{legacy.PackagePrefix + "-server", "analog-bridge", "mmdvm-bridge", "stfu", "ysfgateway"} {
		if !dvPurgeCandidate(n) {
			t.Errorf("%s should be a candidate", n)
		}
	}
	for _, n := range []string{"md380-emu", "qemu-user-static", "asterisk", "asl3", "libc6", "aslcp", "wget", "../x"} {
		if dvPurgeCandidate(n) {
			t.Errorf("%s must never be purged", n)
		}
	}
}

func TestParseRemvLines(t *testing.T) {
	got := ParseRemvLines("Remv stfu [1.0]\nPurg mmdvm-bridge [2]\nInst x (1)\r\nRemv md380-emu [1]\n")
	if strings.Join(got, " ") != "md380-emu mmdvm-bridge stfu" {
		t.Fatalf("%v", got)
	}
}

func TestPurgeLegacyFull(t *testing.T) {
	e := newPurgeEnv(t, []string{"md380-emu", "libfoo", "mmdvm-bridge", "stfu", "qemu-user-static"}, map[string]resp{
		purgeDpkg + "mmdvm-bridge stfu md380-emu qemu-user-static": {out: "mmdvm-bridge\tinstalled\t1\nstfu\tinstalled\t1\nmd380-emu\tinstalled\t1\nqemu-user-static\tinstalled\t1\n"},
		purgeDpkg + "md380-emu":                 {out: "md380-emu\tinstalled\t1\n"},
		"apt-get -s -q purge mmdvm-bridge stfu": {out: "Purg mmdvm-bridge [1]\nRemv stfu [1]\n"},
		"apt-get purge -y":                      {},
	})
	for _, d := range []string{e.o.DVHistoryDir, e.o.DVOldHistoryDir} {
		os.MkdirAll(filepath.Join(d, "x"), 0o700)
		os.WriteFile(filepath.Join(d, "x", "f"), []byte("pw"), 0o600)
	}
	os.WriteFile(e.o.DVKeyring, []byte("key"), 0o644)
	os.WriteFile(e.o.DVSourceList, []byte(dvSourceLine(e.o.DVKeyring)), 0o644)
	os.WriteFile(filepath.Join(e.root, "lists_1"), []byte("x"), 0o644)
	var w bytes.Buffer
	if err := e.o.PurgeLegacy(t.Context(), &w); err != nil {
		t.Fatalf("%v\n%s", err, w.String())
	}
	if e.f.called("apt-get purge -y -q -o DPkg::Lock::Timeout=600 -o Dpkg::Options::=--force-confold -o Dpkg::Use-Pty=0 mmdvm-bridge stfu") != 1 {
		t.Fatalf("calls: %v", e.f.calls)
	}
	if e.f.called("apt-mark manual md380-emu qemu-user-static") != 1 || e.f.called("systemctl disable --now mmdvm_bridge.service") != 1 || e.f.called("systemctl disable --now stfu") != 0 {
		t.Fatalf("calls: %v", e.f.calls)
	}
	for _, p := range []string{e.o.DVHistoryDir, e.o.DVOldHistoryDir} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s still there", p)
		}
	}
	// md380-emu comes from this repository: its source and keyring stay
	for _, p := range []string{e.o.DVSourceList, e.o.DVKeyring, filepath.Join(e.root, "lists_1")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s removed", p)
		}
	}
	m, err := ReadDVMarker(e.o.dvMarkerPath())
	if err != nil || strings.Join(m.Packages, " ") != "libfoo md380-emu qemu-user-static" {
		t.Fatalf("marker %+v %v", m, err)
	}
	// a second run finds nothing to do and calls no apt
	before := len(e.f.calls)
	w.Reset()
	if err := e.o.PurgeLegacy(t.Context(), &w); err != nil || len(e.f.calls) != before || w.Len() != 0 {
		t.Fatalf("second run: %v %d %q", err, len(e.f.calls)-before, w.String())
	}
}

func TestPurgeLegacyRefusesExtraRemovals(t *testing.T) {
	e := newPurgeEnv(t, []string{"md380-emu", "mmdvm-bridge"}, map[string]resp{
		purgeDpkg + "mmdvm-bridge md380-emu qemu-user-static": {out: "mmdvm-bridge\tinstalled\t1\nmd380-emu\tinstalled\t1\n"},
		"apt-get -s -q purge mmdvm-bridge":                    {out: "Remv mmdvm-bridge [1]\nRemv md380-emu [1]\n"},
	})
	os.WriteFile(e.o.DVKeyring, []byte("key"), 0o644)
	os.WriteFile(e.o.DVSourceList, []byte(dvSourceLine(e.o.DVKeyring)), 0o644)
	var w bytes.Buffer
	err := e.o.PurgeLegacy(t.Context(), &w)
	if err == nil || err.Error() != "unexpected_removals" || !strings.Contains(w.String(), "md380-emu") {
		t.Fatalf("%v %s", err, w.String())
	}
	if e.f.called("apt-get purge") != 0 {
		t.Error("purged despite the refusal")
	}
	if _, err := os.Stat(e.o.DVSourceList); err != nil {
		t.Error("source removed although nothing was purged")
	}
	if m, _ := ReadDVMarker(e.o.dvMarkerPath()); len(m.Packages) != 2 {
		t.Error("marker changed")
	}
}

func TestPurgeLegacyNoMarkerDoesNothing(t *testing.T) {
	e := newPurgeEnv(t, nil, nil)
	os.WriteFile(e.o.DVKeyring, []byte("key"), 0o644)
	os.WriteFile(e.o.DVSourceList, []byte(dvSourceLine(e.o.DVKeyring)), 0o644)
	if err := e.o.PurgeLegacy(t.Context(), &bytes.Buffer{}); err != nil || len(e.f.calls) != 0 {
		t.Fatalf("%v %v", err, e.f.calls)
	}
	if _, err := os.Stat(e.o.DVSourceList); err != nil {
		t.Error("source removed without a marker")
	}
	// a marker with only our own packages: still nothing
	e = newPurgeEnv(t, []string{"md380-emu", "qemu-user-static"}, nil)
	if err := e.o.PurgeLegacy(t.Context(), &bytes.Buffer{}); err != nil || len(e.f.calls) != 0 {
		t.Fatalf("%v %v", err, e.f.calls)
	}
}

func TestPurgeLegacyKeepsForeignSource(t *testing.T) {
	e := newPurgeEnv(t, []string{"stfu"}, map[string]resp{
		purgeDpkg + "stfu md380-emu qemu-user-static": {out: "md380-emu\tinstalled\t1\n"},
	})
	os.WriteFile(e.o.DVKeyring, []byte("key"), 0o644)
	os.WriteFile(e.o.DVSourceList, []byte("deb [signed-by=/x] http://192.0.2.1/repo bookworm main\n"), 0o644)
	if err := e.o.PurgeLegacy(t.Context(), &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(e.o.DVSourceList); err != nil {
		t.Error("an operator's source was removed")
	}
	if _, err := os.Stat(e.o.DVKeyring); err != nil {
		t.Error("keyring removed")
	}
}

func TestPurgeLegacyMD380GoneIsAnError(t *testing.T) {
	e := newPurgeEnv(t, []string{"stfu"}, map[string]resp{
		purgeDpkg + "stfu md380-emu qemu-user-static": {out: "stfu\tinstalled\t1\nmd380-emu\tinstalled\t1\n"},
		purgeDpkg + "md380-emu":                       {out: ""},
		"apt-get -s -q purge stfu":                    {out: "Remv stfu [1]\n"},
		"apt-get purge -y":                            {},
	})
	if err := e.o.PurgeLegacy(t.Context(), &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "md380-emu") {
		t.Fatalf("%v", err)
	}
}

func TestPurgeWaitsForDpkg(t *testing.T) {
	e := newPurgeEnv(t, []string{"stfu"}, nil)
	e.f.rules["dpkg-query -W -f=${db:Status-Status} aslcp"] = resp{out: "half-configured"}
	e.o.DVPurgeWait = time.Millisecond
	e.o.DVPurgeSleep = func(time.Duration) { time.Sleep(2 * time.Millisecond) }
	if err := e.o.PurgeLegacy(t.Context(), &bytes.Buffer{}); err == nil {
		t.Fatal("expected a busy error")
	}
	if e.f.called("apt-get") != 0 {
		t.Error("apt ran while dpkg was busy")
	}
}
