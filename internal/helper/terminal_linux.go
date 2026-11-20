//go:build linux

package helper

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// loginPaths are tried in order; the path is never taken from a client.
var loginPaths = []string{"/bin/login", "/usr/bin/login"}

// terminalEnv is the whole environment of login: fixed, nothing from the
// client. login -p keeps it for the user's shell, which then sets its own.
var terminalEnv = []string{
	"TERM=xterm-256color",
	"LANG=C.UTF-8",
	"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
}

const utmpPath = "/var/run/utmp"

// StartLogin starts login(1) on a new PTY (the production StartPTYFunc).
func StartLogin(rows, cols uint16) (PTY, error) {
	var loginPath string
	for _, p := range loginPaths {
		if st, err := os.Stat(p); err == nil && st.Mode().IsRegular() {
			loginPath = p
			break
		}
	}
	if loginPath == "" {
		return nil, errors.New("terminal not available: login not found")
	}
	master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return nil, fmt.Errorf("terminal not available: %w", err)
	}
	fail := func(err error) (PTY, error) {
		master.Close()
		return nil, fmt.Errorf("terminal not available: %w", err)
	}
	// The master's descriptor is only used inside Control: Fd() would put the
	// file in blocking mode and Close could no longer unblock a Read.
	var n int
	if err := ctl(master, func(fd int) (e error) {
		if e = unix.IoctlSetPointerInt(fd, unix.TIOCSPTLCK, 0); e != nil {
			return e
		}
		n, e = unix.IoctlGetInt(fd, unix.TIOCGPTN)
		return e
	}); err != nil {
		return fail(err)
	}
	slavePath := "/dev/pts/" + strconv.Itoa(n)
	slave, err := os.OpenFile(slavePath, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	defer slave.Close()

	s := &loginPTY{master: master, line: "pts/" + strconv.Itoa(n), done: make(chan struct{})}
	if err := s.Resize(rows, cols); err != nil {
		return fail(err)
	}
	cmd := exec.Command(loginPath, "-p")
	cmd.Env = terminalEnv
	cmd.Dir = "/"
	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	// A new session whose controlling terminal is the slave (fd 0): login is
	// its leader, so the session id and process group id are its pid.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
	if err := cmd.Start(); err != nil {
		return fail(err)
	}
	s.pid = cmd.Process.Pid
	go func() {
		_ = cmd.Wait()
		close(s.done)
	}()
	return s, nil
}

func ctl(f *os.File, fn func(fd int) error) error {
	rc, err := f.SyscallConn()
	if err != nil {
		return err
	}
	var ferr error
	if err := rc.Control(func(fd uintptr) { ferr = fn(int(fd)) }); err != nil {
		return err
	}
	return ferr
}

type loginPTY struct {
	master *os.File
	line   string // "pts/N", the utmp line
	pid    int
	done   chan struct{}

	closeOnce sync.Once

	mu   sync.Mutex
	user string
}

func (s *loginPTY) Read(b []byte) (int, error)  { return s.master.Read(b) }
func (s *loginPTY) Write(b []byte) (int, error) { return s.master.Write(b) }
func (s *loginPTY) Done() <-chan struct{}       { return s.done }

func (s *loginPTY) Resize(rows, cols uint16) error {
	return ctl(s.master, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
}

// LinuxUser looks the user up once login has started their shell (a process
// of the session owned by a non-root uid), or in utmp (which also covers a
// root login). Until then it is "".
func (s *loginPTY) LinuxUser() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.user != "" {
		return s.user
	}
	for _, pid := range sessionPIDs(s.pid) {
		if pid == s.pid {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
		if err != nil {
			continue
		}
		uid, ok := parseProcUID(string(b))
		if !ok || uid == 0 {
			continue
		}
		if u, err := user.LookupId(strconv.FormatUint(uint64(uid), 10)); err == nil {
			s.user = u.Username
			return s.user
		}
	}
	if b, err := os.ReadFile(utmpPath); err == nil {
		s.user = utmpUser(b, s.line)
	}
	return s.user
}

// sessionPIDs lists the processes of session sid (all of them, found in /proc).
func sessionPIDs(sid int) []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		b, err := os.ReadFile(filepath.Join("/proc", e.Name(), "stat"))
		if err != nil {
			continue
		}
		// A zombie is dead already; nothing to signal.
		if pid, s, state, ok := parseProcStat(string(b)); ok && s == sid && state != 'Z' {
			out = append(out, pid)
		}
	}
	return out
}

// Close hangs the session up. Closing the master makes the kernel send SIGHUP
// to the foreground process group; the explicit signals cover the rest of the
// process group and of the session (a program that left the foreground).
func (s *loginPTY) Close() error {
	s.closeOnce.Do(func() {
		_ = s.master.Close()
		s.signal(syscall.SIGHUP)
		select {
		case <-s.done:
		case <-time.After(2 * time.Second):
		}
		s.signal(syscall.SIGKILL)
		select {
		case <-s.done:
		case <-time.After(3 * time.Second):
		}
	})
	return nil
}

func (s *loginPTY) signal(sig syscall.Signal) {
	if s.pid <= 0 {
		return
	}
	_ = syscall.Kill(-s.pid, sig)
	for _, pid := range sessionPIDs(s.pid) {
		if pid != s.pid {
			_ = syscall.Kill(pid, sig)
		}
	}
	// The pid itself only while login is not yet reaped: afterwards it could
	// belong to another process (a live process group or session keeps its id
	// from being reused, so the two kills above are safe).
	select {
	case <-s.done:
	default:
		_ = syscall.Kill(s.pid, sig)
	}
}
