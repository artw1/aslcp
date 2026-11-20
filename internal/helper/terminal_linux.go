//go:build linux

package helper

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

// loginPaths are tried in order; the path is never taken from a client.
var loginPaths = []string{"/bin/login", "/usr/bin/login"}

const utmpPath = "/var/run/utmp"

// StartLogin starts login(1) on a new PTY (the production StartPTYFunc). The
// helper allocates the PTY and keeps the master; login itself runs in a
// transient systemd unit with the slave as its terminal (terminal_unit.go),
// so it is neither a child nor in the cgroup of the helper.
func StartLogin(rows, cols uint16) (PTY, error) {
	return startLogin(ExecRunner{}, rows, cols)
}

func startLogin(run Runner, rows, cols uint16) (PTY, error) {
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
	id, err := newTermID()
	if err != nil {
		return nil, fmt.Errorf("terminal not available: %w", err)
	}
	// The host's devpts: the helper has no PrivateDevices, so /dev/ptmx and
	// /dev/pts are the ones systemd (PID 1) sees. PrivateTmp does not touch
	// /dev.
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
	pts := strconv.Itoa(n)
	argv, err := termUnitArgv(id, pts, loginPath)
	if err != nil {
		return fail(err)
	}
	slave, err := os.OpenFile("/dev/pts/"+pts, os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		return fail(err)
	}
	// Held until systemd has started the unit, so the pseudo terminal is
	// never without a slave in between.
	defer slave.Close()

	s := &loginPTY{master: master, run: run, id: id, line: "pts/" + pts,
		done: make(chan struct{}), quit: make(chan struct{})}
	if err := s.Resize(rows, cols); err != nil {
		return fail(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), terminalStartLimit)
	defer cancel()
	out, code, err := run.Run(ctx, argv...)
	if err != nil {
		_ = stopTermUnit(run, id)
		return fail(fmt.Errorf("systemd-run: %w", err))
	}
	if code != 0 {
		return fail(fmt.Errorf("systemd-run failed: %s", LastLines(out, 3)))
	}
	go s.watch()
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

// loginPTY is the helper's side of one terminal: the PTY master and the name
// of the transient unit that runs login on the slave.
type loginPTY struct {
	master *os.File
	run    Runner
	id     string // unit aslcp-term-<id>.service
	line   string // "pts/N", the utmp line

	done      chan struct{} // closed when the unit has ended or the session is closed
	doneOnce  sync.Once
	quit      chan struct{} // stops watch
	closeOnce sync.Once

	mu   sync.Mutex
	user string
}

func (s *loginPTY) Read(b []byte) (int, error)  { return s.master.Read(b) }
func (s *loginPTY) Write(b []byte) (int, error) { return s.master.Write(b) }
func (s *loginPTY) Done() <-chan struct{}       { return s.done }

func (s *loginPTY) finish() { s.doneOnce.Do(func() { close(s.done) }) }

// watch polls the unit until it is inactive. The master's EOF/EIO (every
// slave descriptor closed) ends the relay as well; this covers a background
// process that keeps the slave open after login has exited.
func (s *loginPTY) watch() {
	t := time.NewTicker(terminalPollEvery)
	defer t.Stop()
	for {
		select {
		case <-s.quit:
			return
		case <-t.C:
			if termUnitEnded(s.run, s.id) {
				s.finish()
				return
			}
		}
	}
}

func (s *loginPTY) Resize(rows, cols uint16) error {
	return ctl(s.master, func(fd int) error {
		return unix.IoctlSetWinsize(fd, unix.TIOCSWINSZ, &unix.Winsize{Row: rows, Col: cols})
	})
}

// LinuxUser looks the user up once login has started their shell (a process
// of the unit's cgroup owned by a non-root uid), or in utmp (which also
// covers a root login). Until then it is "".
func (s *loginPTY) LinuxUser() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.user != "" {
		return s.user
	}
	for _, pid := range termUnitPIDs(termUnit(s.id)) {
		b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "status"))
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

// Close ends the session: systemd stops the unit (every process of its
// cgroup), then the master is closed.
func (s *loginPTY) Close() error {
	var err error
	s.closeOnce.Do(func() {
		close(s.quit)
		err = stopTermUnit(s.run, s.id)
		_ = s.master.Close()
		s.finish()
	})
	return err
}

// Release lets go of the terminal without stopping the unit (the helper is
// shutting down). Closing the master hangs the terminal up: login and the
// shell get SIGHUP from the kernel, but the helper kills nothing, and the
// restart of the helper does not take the session's cgroup with it.
func (s *loginPTY) Release() error {
	s.closeOnce.Do(func() {
		close(s.quit)
		_ = s.master.Close()
		s.finish()
	})
	return nil
}
