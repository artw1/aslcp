//go:build linux

package helper

import (
	"os"
	"strings"
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

// TestStartLoginPTY runs a real pseudo terminal. It needs root (the helper's
// own situation) and a shell to stand in for login(1), whose PAM prompts need
// a real account.
func TestStartLoginPTY(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/dev/ptmx"); err != nil {
		t.Skip("no /dev/ptmx")
	}
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh")
	}
	old := loginPaths
	loginPaths = []string{"/bin/sh"}
	defer func() { loginPaths = old }()

	p, err := StartLogin(24, 80)
	if err != nil {
		t.Skipf("cannot allocate a terminal here: %v", err)
	}
	defer p.Close()
	lp := p.(*loginPTY)

	// The terminal is the controlling terminal and has the size asked for.
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
	// A background child dies with the session.
	if _, err := p.Write([]byte("sleep 300 >/dev/null 2>&1 &\n")); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	pid := lp.pid
	_ = p.Close()
	select {
	case <-p.Done():
	default:
		t.Fatal("login process not reaped after Close")
	}
	if err := syscall.Kill(-pid, 0); err == nil {
		t.Fatal("process group still alive after Close")
	}
	if n := len(sessionPIDs(pid)); n != 0 {
		t.Fatalf("%d processes of the session survive", n)
	}
}
