//go:build linux

package helper

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// readUntil reads from p until the output contains want or the time is up.
func readUntil(t *testing.T, p PTY, want string) string {
	t.Helper()
	var sb strings.Builder
	deadline := time.Now().Add(10 * time.Second)
	buf := make([]byte, 4096)
	type res struct {
		n   int
		err error
	}
	for !strings.Contains(sb.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("timeout waiting for %q, got %q", want, sb.String())
		}
		ch := make(chan res, 1)
		go func() { n, err := p.Read(buf); ch <- res{n, err} }()
		select {
		case r := <-ch:
			sb.Write(buf[:r.n])
			if r.err != nil {
				if strings.Contains(sb.String(), want) {
					return sb.String()
				}
				t.Fatalf("read: %v, got %q", r.err, sb.String())
			}
		case <-time.After(time.Until(deadline)):
			t.Fatalf("timeout waiting for %q, got %q", want, sb.String())
		}
	}
	return sb.String()
}

// fakeSystemd stands in for systemd: "systemd-run" starts /bin/sh as a new
// session with the --property=TTYPath tty as its controlling terminal (what
// the real unit does for login), "systemctl stop" kills that process group.
type fakeSystemd struct {
	mu    sync.Mutex
	argv  []string
	cmd   *exec.Cmd
	stops int
	tty   *os.File
}

func (f *fakeSystemd) Run(_ context.Context, argv ...string) (string, int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case argv[0] == "systemd-run":
		f.argv = argv
		var tty string
		for _, a := range argv {
			if v, ok := strings.CutPrefix(a, "--property=TTYPath="); ok {
				tty = v
			}
		}
		slave, err := os.OpenFile(tty, os.O_RDWR|syscall.O_NOCTTY, 0)
		if err != nil {
			return err.Error(), 1, nil
		}
		f.tty = slave
		cmd := exec.Command("/bin/sh")
		cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
		if err := cmd.Start(); err != nil {
			return err.Error(), 1, nil
		}
		f.cmd = cmd
		go cmd.Wait()
		return "", 0, nil
	case argv[0] == "systemctl" && argv[1] == "stop":
		f.stops++
		if f.cmd != nil {
			_ = syscall.Kill(-f.cmd.Process.Pid, syscall.SIGKILL)
		}
		return "", 0, nil
	case argv[0] == "systemctl" && argv[1] == "show":
		if f.cmd != nil && syscall.Kill(f.cmd.Process.Pid, 0) == nil {
			return "active\n", 0, nil
		}
		return "inactive\n", 0, nil
	}
	return "", 1, nil
}

// TestStartLoginPTY allocates a real pseudo terminal in the host /dev/pts and
// runs a shell on its slave through a fake systemd, so size, resize, relay
// and stop are exercised without systemd or login(1).
func TestStartLoginPTY(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	old := loginPaths
	loginPaths = []string{"/bin/sh"}
	defer func() { loginPaths = old }()

	fs := &fakeSystemd{}
	p, err := startLogin(fs, 24, 80)
	if err != nil {
		t.Skipf("cannot allocate a terminal here: %v", err)
	}
	defer p.Close()
	defer func() {
		if fs.tty != nil {
			fs.tty.Close()
		}
	}()
	lp := p.(*loginPTY)
	if !termIDRe.MatchString(lp.id) || !strings.HasPrefix(lp.line, "pts/") {
		t.Fatalf("id %q line %q", lp.id, lp.line)
	}
	want := "--property=TTYPath=/dev/" + lp.line
	found := false
	for _, a := range fs.argv {
		found = found || a == want
	}
	if !found {
		t.Fatalf("argv has no %s: %q", want, fs.argv)
	}

	if _, err := p.Write([]byte("echo SZ=$(stty size)\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "SZ=24 80")
	if err := p.Resize(40, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Write([]byte("echo SZ=$(stty size)\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, p, "SZ=40 100")

	pid := fs.cmd.Process.Pid
	_ = p.Close()
	if fs.stops != 1 {
		t.Fatalf("systemctl stop called %d times", fs.stops)
	}
	select {
	case <-p.Done():
	default:
		t.Fatal("Done not closed after Close")
	}
	_ = p.Close() // twice is fine
	if fs.stops != 1 {
		t.Fatalf("second Close stopped again (%d)", fs.stops)
	}
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(-pid, 0) == nil && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(-pid, 0) == nil {
		t.Fatal("process group still alive after Close")
	}
}

// Release closes the terminal and leaves the unit alone.
func TestLoginPTYReleaseKeepsUnit(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	old := loginPaths
	loginPaths = []string{"/bin/sh"}
	defer func() { loginPaths = old }()
	fs := &fakeSystemd{}
	p, err := startLogin(fs, 24, 80)
	if err != nil {
		t.Skipf("cannot allocate a terminal here: %v", err)
	}
	defer func() {
		if fs.cmd != nil {
			_ = syscall.Kill(-fs.cmd.Process.Pid, syscall.SIGKILL)
		}
		if fs.tty != nil {
			fs.tty.Close()
		}
	}()
	_ = p.Release()
	_ = p.Close() // a Close after Release does nothing more
	if fs.stops != 0 {
		t.Fatalf("Release stopped the unit (%d stops)", fs.stops)
	}
}

// A failing systemd-run is an error and leaves no terminal behind.
func TestStartLoginSystemdRunFails(t *testing.T) {
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	old := loginPaths
	loginPaths = []string{"/bin/sh"}
	defer func() { loginPaths = old }()
	r := &fakeRunner{rules: map[string]resp{"systemd-run": {out: "Failed to start transient service unit\n", code: 1}}}
	if _, err := startLogin(r, 24, 80); err == nil || !strings.Contains(err.Error(), "systemd-run failed") {
		t.Fatalf("err = %v", err)
	}
}
