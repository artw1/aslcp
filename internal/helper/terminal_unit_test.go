package helper

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestTermUnitArgv(t *testing.T) {
	got, err := termUnitArgv("0123456789abcdef", "7", "/bin/login")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"systemd-run", "--quiet",
		"--unit=aslcp-term-0123456789abcdef",
		"--description=ASLCP web terminal",
		"--service-type=exec", "--collect",
		"--property=TTYPath=/dev/pts/7",
		"--property=StandardInput=tty", "--property=StandardOutput=tty", "--property=StandardError=tty",
		"--property=TTYReset=yes", "--property=TTYVHangup=yes",
		"--property=UtmpIdentifier=ts/7", "--property=UtmpMode=login",
		"--property=TimeoutStopSec=5s",
		"--setenv=TERM=xterm-256color",
		"--setenv=LANG=C.UTF-8",
		"--setenv=PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"--", "/bin/login", "-p",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv:\n got %q\nwant %q", got, want)
	}
	// No sandbox property may sneak in.
	for _, a := range got {
		for _, bad := range []string{"PrivateDevices", "PrivateTmp", "ProtectHome", "ProtectSystem", "NoNewPrivileges", "User="} {
			if strings.Contains(a, bad) {
				t.Errorf("sandbox property in argv: %s", a)
			}
		}
	}
}

func TestUtmpIdent(t *testing.T) {
	for pts, want := range map[string]string{"0": "ts/0", "12": "s/12", "123": "/123", "1234": "1234", "1234567": "4567"} {
		if got := utmpIdent(pts); got != want {
			t.Errorf("utmpIdent(%s) = %q, want %q", pts, got, want)
		}
		if len(utmpIdent(pts)) > 4 {
			t.Errorf("identifier too long for %s", pts)
		}
	}
}

func TestTermUnitValidation(t *testing.T) {
	good := "00ff00ff00ff00ff"
	for _, id := range []string{"", "xyz", "0123456789ABCDEF", "0123456789abcde", "0123456789abcdef0", "0123456789abcde;", "../../etc/passwd", "0123456789abcde\n"} {
		if _, err := termUnitArgv(id, "1", "/bin/login"); err == nil {
			t.Errorf("id %q accepted", id)
		}
		if err := stopTermUnit(&fakeRunner{}, id); err == nil {
			t.Errorf("stop accepted id %q", id)
		}
	}
	for _, pts := range []string{"", "x", "-1", "1 2", "1,TTYPath=/dev/null", "12345678", "1/../2", "+3"} {
		if _, err := termUnitArgv(good, pts, "/bin/login"); err == nil {
			t.Errorf("pts %q accepted", pts)
		}
	}
	for _, lp := range []string{"", "login", "/bin/login -x", "/bin/lo=gin"} {
		if _, err := termUnitArgv(good, "1", lp); err == nil {
			t.Errorf("login path %q accepted", lp)
		}
	}
	id, err := newTermID()
	if err != nil || !termIDRe.MatchString(id) {
		t.Fatalf("generated id %q: %v", id, err)
	}
	id2, _ := newTermID()
	if id == id2 {
		t.Fatal("ids repeat")
	}
}

func TestStopTermUnit(t *testing.T) {
	id := "0123456789abcdef"
	r := &fakeRunner{rules: map[string]resp{"systemctl stop aslcp-term-0123456789abcdef.service": {}}}
	if err := stopTermUnit(r, id); err != nil {
		t.Fatal(err)
	}
	if r.called("systemctl stop aslcp-term-0123456789abcdef.service") != 1 {
		t.Fatalf("calls: %v", r.calls)
	}
	// already gone
	r = &fakeRunner{rules: map[string]resp{"systemctl stop": {out: "Failed to stop x.service: Unit x.service not loaded.\n", code: 5}}}
	if err := stopTermUnit(r, id); err != nil {
		t.Fatalf("a unit that is gone is not an error: %v", err)
	}
	// a real failure and a command that cannot run
	r = &fakeRunner{rules: map[string]resp{"systemctl stop": {out: "Access denied\n", code: 1}}}
	if err := stopTermUnit(r, id); err == nil {
		t.Fatal("failure not reported")
	}
	r = &fakeRunner{rules: map[string]resp{"systemctl stop": {err: errors.New("not found")}}}
	if err := stopTermUnit(r, id); err == nil {
		t.Fatal("run error not reported")
	}
}

func TestTermUnitEnded(t *testing.T) {
	id := "0123456789abcdef"
	for out, want := range map[string]bool{"active\n": false, "activating\n": false, "deactivating\n": false, "inactive\n": true, "failed\n": true, "": false} {
		r := &fakeRunner{rules: map[string]resp{"systemctl show -p ActiveState --value aslcp-term-" + id: {out: out}}}
		if got := termUnitEnded(r, id); got != want {
			t.Errorf("%q: got %v, want %v", out, got, want)
		}
	}
	// when systemctl cannot say, the unit is not declared over
	r := &fakeRunner{rules: map[string]resp{"systemctl show": {err: errors.New("boom")}}}
	if termUnitEnded(r, id) {
		t.Error("an error counted as ended")
	}
}

// fakeCgroup points cgroupRoots and procRoot at a temp tree.
func fakeCgroup(t *testing.T, unit string, procs map[int]string) {
	t.Helper()
	dir := t.TempDir()
	oldC, oldP := cgroupRoots, procRoot
	cgroupRoots = []string{filepath.Join(dir, "cg")}
	procRoot = filepath.Join(dir, "proc")
	t.Cleanup(func() { cgroupRoots, procRoot = oldC, oldP })
	var list strings.Builder
	for pid, comm := range procs {
		d := filepath.Join(procRoot, strconv.Itoa(pid))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(d, "comm"), []byte(comm+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		list.WriteString(strconv.Itoa(pid) + "\n")
	}
	if err := os.MkdirAll(filepath.Join(cgroupRoots[0], unit), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cgroupRoots[0], unit, "cgroup.procs"), []byte(list.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTermUnitPIDsAndBusy(t *testing.T) {
	unit := "aslcp-term-0123456789abcdef.service"
	fakeCgroup(t, unit, map[int]string{4: "login", 5: "bash"})
	if got := termUnitPIDs(unit); len(got) != 2 {
		t.Fatalf("pids %v", got)
	}
	if termUnitBusy(unit) {
		t.Fatal("login and bash are not a package manager")
	}
	if termUnitPIDs("../x") != nil || termUnitPIDs("aslcp-term-zz.service") != nil {
		t.Fatal("invalid unit name accepted")
	}
	if termUnitPIDs("aslcp-term-ffffffffffffffff.service") != nil {
		t.Fatal("missing cgroup returned pids")
	}
}

func TestTermUnitBusyApt(t *testing.T) {
	unit := "aslcp-term-0123456789abcdef.service"
	fakeCgroup(t, unit, map[int]string{4: "login", 5: "bash", 6: "dpkg"})
	if !termUnitBusy(unit) {
		t.Fatal("dpkg in the unit is not busy")
	}
}

const listTwo = "aslcp-term-00000000000000aa.service loaded active running ASLCP web terminal\n" +
	"aslcp-term-00000000000000bb.service loaded active running ASLCP web terminal\n" +
	"not-ours.service loaded active running x\n" +
	"aslcp-term-../../x.service loaded active running y\n"

func TestSweepStopsLeftovers(t *testing.T) {
	r := &fakeRunner{rules: map[string]resp{
		"systemctl list-units": {out: listTwo},
		"systemctl stop":       {},
	}}
	stopped, left, err := SweepTermUnits(context.Background(), r, func(string) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 2 || len(left) != 0 {
		t.Fatalf("stopped %v left %v", stopped, left)
	}
	if r.called("systemctl stop aslcp-term-00000000000000aa.service") != 1 || r.called("systemctl stop aslcp-term-00000000000000bb.service") != 1 {
		t.Fatalf("calls: %v", r.calls)
	}
	if r.called("systemctl stop not-ours") != 0 || r.called("systemctl stop aslcp-term-../") != 0 {
		t.Fatalf("stopped a unit that is not a terminal unit: %v", r.calls)
	}
}

// A unit running apt or dpkg is not cut off by the sweep (the new helper must
// not kill the upgrade that restarted it).
func TestSweepSparesPackageManager(t *testing.T) {
	r := &fakeRunner{rules: map[string]resp{
		"systemctl list-units": {out: listTwo},
		"systemctl stop":       {},
	}}
	busy := func(u string) bool { return strings.Contains(u, "00aa") }
	stopped, left, err := SweepTermUnits(context.Background(), r, busy)
	if err != nil {
		t.Fatal(err)
	}
	if len(stopped) != 1 || stopped[0] != "aslcp-term-00000000000000bb.service" || len(left) != 1 || left[0] != "aslcp-term-00000000000000aa.service" {
		t.Fatalf("stopped %v left %v", stopped, left)
	}
	if r.called("systemctl stop aslcp-term-00000000000000aa.service") != 0 {
		t.Fatalf("stopped the unit that runs apt: %v", r.calls)
	}
}

func TestSweepWithRealBusyCheck(t *testing.T) {
	unit := "aslcp-term-00000000000000aa.service"
	fakeCgroup(t, unit, map[int]string{4: "login", 6: "apt-get"})
	r := &fakeRunner{rules: map[string]resp{
		"systemctl list-units": {out: listTwo},
		"systemctl stop":       {},
	}}
	stopped, left, err := SweepTermUnits(context.Background(), r, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0] != unit || len(stopped) != 1 {
		t.Fatalf("stopped %v left %v", stopped, left)
	}
}

func TestSweepErrors(t *testing.T) {
	r := &fakeRunner{rules: map[string]resp{"systemctl list-units": {err: errors.New("no systemd")}}}
	if _, _, err := SweepTermUnits(context.Background(), r, nil); err == nil {
		t.Fatal("list error not reported")
	}
	r = &fakeRunner{rules: map[string]resp{
		"systemctl list-units": {out: listTwo},
		"systemctl stop":       {out: "denied", code: 1},
	}}
	stopped, _, err := SweepTermUnits(context.Background(), r, func(string) bool { return false })
	if err == nil || len(stopped) != 0 {
		t.Fatalf("stopped %v err %v", stopped, err)
	}
	if r.called("systemctl stop") != 2 {
		t.Fatalf("one failure must not stop the sweep: %v", r.calls)
	}
}

// The sweep runs before the first connection is accepted, so a unit started
// by a terminal.open can never be mistaken for a leftover.
func TestServeSweepsBeforeAccepting(t *testing.T) {
	ln, err := net.Listen("unix", filepath.Join(t.TempDir(), "s"))
	if err != nil {
		t.Skip(err)
	}
	defer ln.Close()
	swept := false
	srv := &Server{SweepTerminals: func(context.Context) { swept = true }}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := srv.Serve(ctx, ln); err != nil {
		t.Fatal(err)
	}
	if !swept {
		t.Fatal("no sweep at start")
	}
}
