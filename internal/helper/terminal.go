package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// Browser terminal (docs/PLAN-1.3.0.md section 8). The helper starts login(1)
// on a pseudo terminal and relays framed bytes (terminal_frame.go) over the
// connection that carried the terminal.open request. No shell is started by
// ASLCP: login asks PAM to authenticate a Linux user, then drops to it.

// OpTerminalOpen opens a terminal session. After the JSON reply the
// connection is a raw framed stream until either side closes it.
const OpTerminalOpen = "terminal.open"

// TerminalRequest is the argument of terminal.open. Both sizes are optional
// (zero: 24 rows, 80 columns) and bounded; nothing else is accepted, so no
// client string reaches the command line or the environment of login.
type TerminalRequest struct {
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
}

// PTY is a started login session on a pseudo terminal.
type PTY interface {
	// Read returns what the session writes; it fails once the session has
	// ended and its output is consumed.
	io.ReadWriter
	// Resize sets the window size (already validated).
	Resize(rows, cols uint16) error
	// LinuxUser returns the authenticated Linux user, or "" while unknown.
	LinuxUser() string
	// Done is closed when the login process has exited.
	Done() <-chan struct{}
	// Close ends the session: closes the terminal, sends SIGHUP to the
	// session's process group, then SIGKILL after a short grace, and reaps
	// the child. Safe to call more than once.
	Close() error
}

// StartPTYFunc starts a login session of the given (valid) size.
type StartPTYFunc func(rows, cols uint16) (PTY, error)

const (
	// terminalWriteTimeout bounds one write towards the web server.
	terminalWriteTimeout = 30 * time.Second
	// terminalExitGrace is how long output may still drain after login exits
	// before the terminal is closed (a background process can keep the slave
	// open).
	terminalExitGrace = 500 * time.Millisecond
	terminalUserTries = 300
)

// terminalUserPoll is how often the Linux user is looked up while unknown.
const terminalUserPoll = time.Second

func (s *Server) handleTerminal(ctx context.Context, conn net.Conn, in io.Reader, req Request, uid uint32) {
	reply := func(v any) {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = json.NewEncoder(conn).Encode(v)
	}
	tr := req.Terminal
	if tr == nil {
		tr = &TerminalRequest{}
	}
	rows, cols := tr.Rows, tr.Cols
	if (rows != 0 || cols != 0) && !ValidSize(rows, cols) {
		s.logf("uid %d: %s: invalid size", uid, req.Op)
		reply(errorResponse{Error: "invalid size"})
		return
	}
	rows, cols = ClampSize(rows, cols)
	if s.StartTerminal == nil {
		reply(errorResponse{Error: "terminal not available"})
		return
	}
	p, err := s.StartTerminal(rows, cols)
	if err != nil {
		s.logf("uid %d: %s: error: %v", uid, req.Op, err)
		reply(errorResponse{Error: err.Error()})
		return
	}
	// Session start and end are logged, never what passes through.
	s.logf("uid %d: %s started", uid, req.Op)
	started := time.Now()
	// The request read set short deadlines; a session has none.
	_ = conn.SetDeadline(time.Time{})
	reply(OKResult{OK: true})
	_ = conn.SetWriteDeadline(time.Time{})
	relayTerminal(ctx, conn, in, p, terminalUserPoll)
	s.logf("uid %d: %s ended after %s", uid, req.Op, time.Since(started).Round(time.Second))
}

// relayTerminal moves frames between conn and the PTY until either side ends
// or ctx is done, then closes both and reaps the child.
func relayTerminal(ctx context.Context, conn net.Conn, in io.Reader, p PTY, userPoll time.Duration) {
	in = &skipLeadingSpace{r: in} // the newline of the request; frame types are 0..2
	var wmu sync.Mutex
	write := func(typ byte, b []byte) error {
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(terminalWriteTimeout))
		if typ == FrameData {
			return WriteData(conn, b)
		}
		return WriteFrame(conn, typ, b)
	}

	stop := make(chan struct{})
	var once sync.Once
	end := func() { once.Do(func() { close(stop) }) }
	var wg sync.WaitGroup

	// Web server to PTY.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer end()
		for {
			typ, payload, err := ReadFrame(in)
			if err != nil {
				return
			}
			switch typ {
			case FrameData:
				if _, err := p.Write(payload); err != nil {
					return
				}
			case FrameResize:
				rows, cols, err := ParseResize(payload)
				if err != nil {
					return
				}
				rows, cols = ClampSize(rows, cols)
				_ = p.Resize(rows, cols)
			default:
				return // FrameUser only flows towards the web server
			}
		}
	}()

	// PTY to web server.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer end()
		buf := make([]byte, 16<<10)
		for {
			n, err := p.Read(buf)
			if n > 0 {
				if werr := write(FrameData, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Linux user, once known.
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(userPoll)
		defer t.Stop()
		for i := 0; i < terminalUserTries; i++ {
			select {
			case <-stop:
				return
			case <-t.C:
			}
			if u := p.LinuxUser(); u != "" {
				_ = write(FrameUser, []byte(u))
				return
			}
		}
	}()

	// Login exited: let the output drain, then close the terminal.
	go func() {
		select {
		case <-p.Done():
			select {
			case <-stop:
			case <-time.After(terminalExitGrace):
			}
			end()
		case <-stop:
		}
	}()

	select {
	case <-stop:
	case <-ctx.Done():
	}
	end()
	_ = p.Close() // closes the terminal (unblocks the reader), HUP, kill, reap
	_ = conn.Close()
	wg.Wait()
}

// errTerminalUnsupported is returned where there is no pseudo terminal.
var errTerminalUnsupported = errors.New("terminal not available on this system")
