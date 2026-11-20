package helper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/user"
	"strconv"
	"sync/atomic"
	"time"
)

// Server accepts connections and serves requests.
type Server struct {
	Ops *Ops
	// Peer returns the uid of the process on the other end of conn.
	Peer func(conn net.Conn) (uint32, error)
	// AllowedUIDs returns the uids permitted to talk to the helper (root plus
	// the aslcp user). It is evaluated per connection.
	AllowedUIDs func() []uint32
	// Idle is how long the server waits without connections before Serve
	// returns (systemd restarts the service on the next connection). Zero
	// means never.
	Idle   time.Duration
	Logger *log.Logger
	// StartTerminal starts a login session for terminal.open (nil: the op
	// answers "terminal not available"). NewServer sets StartLogin.
	StartTerminal StartPTYFunc
	// SweepTerminals runs once when Serve starts, before the first
	// connection is accepted (nil: nothing). NewServer sets it to stop the
	// terminal units an earlier helper left (SweepTermUnits).
	SweepTerminals func(ctx context.Context)

	active atomic.Int32
}

// NewServer returns a Server wired to the real system.
func NewServer(logger *log.Logger, extraUIDs ...uint32) *Server {
	return &Server{
		Ops:         NewOps(),
		Peer:        PeerUID,
		AllowedUIDs: func() []uint32 { return DefaultAllowedUIDs(extraUIDs...) },
		Idle:        60 * time.Second,
		Logger:      logger,

		StartTerminal:  StartLogin,
		SweepTerminals: sweepTerminalsAtStart(logger),
	}
}

// DefaultAllowedUIDs returns root, the uid of user "aslcp" (if it exists)
// and any extra uids.
func DefaultAllowedUIDs(extra ...uint32) []uint32 {
	uids := []uint32{0}
	if u, err := user.Lookup("aslcp"); err == nil {
		if n, err := strconv.ParseUint(u.Uid, 10, 32); err == nil {
			uids = append(uids, uint32(n))
		}
	}
	return append(uids, extra...)
}

// Authorized reports whether uid is in allowed.
func Authorized(uid uint32, allowed []uint32) bool {
	for _, a := range allowed {
		if a == uid {
			return true
		}
	}
	return false
}

// maxRequest bounds requests of the plain operations; config operations may
// carry files and are bounded by maxConfigRequest.
const maxRequest = 4096

// Serve accepts connections on ln until ctx is done or the idle timeout
// passes with no connection and no request in flight.
func (s *Server) Serve(ctx context.Context, ln net.Listener) error {
	// Before any connection: a unit started after this point is not a leftover.
	if s.SweepTerminals != nil {
		s.SweepTerminals(ctx)
	}
	type deadliner interface{ SetDeadline(time.Time) error }
	dl, _ := ln.(deadliner)
	last := time.Now()
	for {
		if ctx.Err() != nil {
			s.drain()
			return nil
		}
		if dl != nil {
			_ = dl.SetDeadline(time.Now().Add(time.Second))
		}
		conn, err := ln.Accept()
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				if s.Idle > 0 && s.active.Load() == 0 && time.Since(last) > s.Idle {
					s.logf("idle for %s, exiting", s.Idle)
					return nil
				}
				continue
			}
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				s.drain()
				return nil
			}
			return err
		}
		last = time.Now()
		s.active.Add(1)
		go func() {
			defer s.active.Add(-1)
			s.handle(ctx, conn)
		}()
	}
}

// drainTimeout bounds how long a stopping helper waits for requests in
// flight. The package's postinst restarts the helper during an upgrade while
// the web UI is polling it; answering those requests instead of dropping the
// connection avoids "unexpected end of JSON" errors in the UI.
var drainTimeout = 20 * time.Second

func (s *Server) drain() {
	deadline := time.Now().Add(drainTimeout)
	for s.active.Load() > 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
}

func (s *Server) logf(format string, a ...any) {
	if s.Logger != nil {
		s.Logger.Printf(format, a...)
	}
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	reply := func(v any) {
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		_ = json.NewEncoder(conn).Encode(v)
	}
	fail := func(msg string) { reply(errorResponse{Error: msg}) }

	// The request is read before the peer check so that a rejected client has
	// no unread data pending (which would turn our reply into a reset).
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var req Request
	dec := json.NewDecoder(io.LimitReader(conn, maxConfigRequest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		s.logf("bad request: %v", err)
		drainUnread(conn, nil)
		fail("invalid request")
		return
	}
	if req.Op != OpBackupRestore && req.Op != OpTerminalOpen {
		// Only backup-restore and terminal.open have more to say after the request. Anything
		// else left in the receive buffer (the encoder's trailing newline)
		// would turn the close after our reply into a connection reset.
		drainUnread(conn, dec.Buffered())
	}
	if !isConfigOp(req.Op) && req.Op != OpDiagCollect && dec.InputOffset() > maxRequest {
		s.logf("request too large for op %s", safeOp(req.Op))
		fail("invalid request")
		return
	}
	uid, err := s.Peer(conn)
	if err != nil {
		s.logf("rejecting connection: peer credentials: %v", err)
		fail("peer check failed")
		return
	}
	if !Authorized(uid, s.AllowedUIDs()) {
		s.logf("rejecting connection from uid %d", uid)
		fail("not authorized")
		return
	}

	if req.Op == OpTerminalOpen {
		// Frames follow the request on the same connection (terminal.go).
		s.handleTerminal(ctx, conn, io.MultiReader(dec.Buffered(), conn), req, uid)
		return
	}
	if isStreamOp(req.Op) {
		// The archive of backup-restore follows the request; the decoder may
		// already have buffered the first bytes of it. The client's JSON
		// encoder terminates the request with a newline, which is not part
		// of the archive, so leading JSON whitespace is skipped (lazily: a
		// stream op that sends nothing more must not block here).
		rest := &skipLeadingSpace{r: io.MultiReader(dec.Buffered(), conn)}
		s.handleStream(ctx, conn, rest, req, uid)
		return
	}

	// Not derived from ctx: a request in flight when the helper is asked to
	// stop still completes (see drain).
	timeout := callTimeout
	if req.Op == OpOSCheck {
		timeout = osCallTimeout
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	res, err := s.Ops.HandleRequest(cctx, req)
	if err != nil {
		if req.Op == "" || !knownOp(req.Op) {
			s.logf("uid %d: refused unknown op %s", uid, safeOp(req.Op))
			fail("unknown op")
			return
		}
		s.logf("uid %d: %s: error: %v", uid, req.Op, err)
		fail(err.Error())
		return
	}
	s.logf("uid %d: %s ok", uid, req.Op)
	reply(res)
}

// drainTimeoutUnread bounds how long drainUnread waits for bytes that are not
// there yet.
const drainTimeoutUnread = 100 * time.Millisecond

// drainUnread consumes what the client sent after the request, so that
// closing the connection does not reset it: on a unix stream socket, close
// with unread data makes the peer's read fail with ECONNRESET and the peer
// loses our reply. json.Decoder stops at the closing brace, so the newline
// the client's encoder appends can still be queued, depending on where the
// read boundaries fell. buffered is what the decoder already read past the
// request (nil after a failed decode). It never blocks on a client that sends
// nothing more: it returns at EOF (the client closed its write side), at the
// first timeout, or after a bounded amount of data.
func drainUnread(conn net.Conn, buffered io.Reader) {
	if buffered != nil {
		// A trailing newline the decoder already read is the whole of what
		// the encoder appends: nothing is left to wait for.
		b, _ := io.ReadAll(io.LimitReader(buffered, 1<<20))
		if len(b) > 0 && isJSONSpace(b[len(b)-1]) {
			return
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(drainTimeoutUnread))
	_, _ = io.Copy(io.Discard, io.LimitReader(conn, 1<<20))
}

func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\r' || c == '\n' }

// skipLeadingSpace drops JSON whitespace (space, tab, CR, LF) from the start
// of the stream on the first Read. gzip's magic (0x1f 0x8b) is not
// whitespace, so no archive byte is ever lost.
type skipLeadingSpace struct {
	r    io.Reader
	done bool
}

func (s *skipLeadingSpace) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for !s.done {
		n, err := s.r.Read(p)
		i := 0
		for i < n && (p[i] == ' ' || p[i] == '\t' || p[i] == '\r' || p[i] == '\n') {
			i++
		}
		if i < n {
			s.done = true
			copy(p, p[i:n])
			return n - i, err
		}
		if err != nil {
			return 0, err
		}
	}
	return s.r.Read(p)
}

func knownOp(op string) bool {
	if isSysOp(op) || op == OpDiagCollect {
		return true
	}
	switch op {
	case OpUpdateStatus, OpUpdateCheck, OpUpdateApply, OpOSCheck, OpOSStatus, OpOSApply, OpReboot, OpAslcpRestart, OpDVSwitchStatus, OpDVSwitchJob, OpDVSwitchInstall, OpDVSwitchUninstall, OpDVSwitchRestoreDiscard, OpDVSwitchSetup, OpDVSwitchInfo, OpDVSwitchDMRStatus, OpDVSwitchIdentity, OpDVSwitchCredentials, OpDVSwitchModes, OpDVSwitchStartup, OpDVSwitchRollback, OpDVSwitchRestart, OpDVSwitchLists, OpDVSwitchTune, OpDVSwitchUSRP, OpDVSwitchPrivate, OpDVSwitchBM, OpDVSwitchSTFUInstall, OpFirewallStatus, OpFirewallOpen, OpChannelStable, OpChannelBeta, OpAsteriskRestart, OpConfigList, OpConfigRead, OpConfigApply, OpConfigLog, OpConfigShow, OpConfigRestore, OpConfigRecord, OpBackupInfo, OpBackupCreate, OpBackupRestore:
		return true
	}
	return false
}

// safeOp limits what a client-supplied string can put into the log.
func safeOp(op string) string {
	if len(op) > 40 {
		op = op[:40]
	}
	return fmt.Sprintf("%q", op)
}

// ListenerFromSystemd returns the listener passed by systemd socket
// activation (LISTEN_PID/LISTEN_FDS, fd 3), or (nil, nil) when the process
// was not socket-activated.
func ListenerFromSystemd() (net.Listener, error) {
	pid, _ := strconv.Atoi(os.Getenv("LISTEN_PID"))
	n, _ := strconv.Atoi(os.Getenv("LISTEN_FDS"))
	os.Unsetenv("LISTEN_PID")
	os.Unsetenv("LISTEN_FDS")
	os.Unsetenv("LISTEN_FDNAMES")
	if pid != os.Getpid() || n < 1 {
		return nil, nil
	}
	f := os.NewFile(3, "systemd-socket")
	defer f.Close()
	return net.FileListener(f)
}

// ListenUnix creates a unix socket at path with mode 0660 (for tests and
// --listen), replacing a stale socket file.
func ListenUnix(path string) (net.Listener, error) {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o660); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}
