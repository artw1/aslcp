// Package amitest provides a reusable fake Asterisk Manager Interface server
// for tests. It listens on 127.0.0.1, sends the banner, checks Login, routes
// actions to registered handlers, can push events, drop connections, and
// answer without an ActionID (as app_rpt's RptLocalNodes does).
//
// It deliberately does not import the ami package, so any package can use it.
package amitest

import (
	"bufio"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// Request is a parsed client action.
type Request struct {
	Action   string
	ActionID string
	Fields   [][2]string // all headers in order, including Action and ActionID
}

// Get returns the first header value named key (case-insensitive).
func (r *Request) Get(key string) string {
	for _, f := range r.Fields {
		if strings.EqualFold(f[0], key) {
			return f[1]
		}
	}
	return ""
}

// Handler produces the raw wire reply for a request. Returning "" sends
// nothing (useful to test timeouts). Handlers run on the session's read
// goroutine, so a slow handler delays later requests on that session.
type Handler func(s *Session, req *Request) string

// Session is one accepted client connection.
type Session struct {
	srv  *Server
	conn net.Conn
	wmu  sync.Mutex
}

// Write sends raw text to the client.
func (s *Session) Write(text string) {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	_, _ = s.conn.Write([]byte(text))
}

// Close drops the connection.
func (s *Session) Close() { s.conn.Close() }

// Server is the fake AMI server.
type Server struct {
	t           testing.TB
	Addr        string
	Username    string
	Secret      string
	mu          sync.Mutex
	ln          net.Listener
	handlers    map[string]Handler
	sessions    []*Session
	requests    []Request
	banner      string
	loginEvents []string
	ignorePing  bool
	echoSecret  bool
	wg          sync.WaitGroup
	closed      bool
}

// New starts a server with Username "admin" and Secret "s3cret!". It is
// closed automatically at test cleanup.
func New(t testing.TB) *Server {
	t.Helper()
	s := &Server{t: t, Username: "admin", Secret: "s3cret!", handlers: map[string]Handler{}}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("amitest: listen: %v", err)
	}
	s.ln = ln
	s.Addr = ln.Addr().String()
	s.wg.Add(1)
	go s.acceptLoop(ln)
	t.Cleanup(s.Close)
	return s
}

// Handle registers a handler for an action name (case-insensitive).
func (s *Server) Handle(action string, h Handler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handlers[strings.ToLower(action)] = h
}

// SetBanner overrides the greeting line (default "Asterisk Call Manager/9.0.0").
func (s *Server) SetBanner(b string) {
	s.mu.Lock()
	s.banner = b
	s.mu.Unlock()
}

// IgnorePing makes the server silently ignore Ping actions.
func (s *Server) IgnorePing(v bool) {
	s.mu.Lock()
	s.ignorePing = v
	s.mu.Unlock()
}

// EchoSecretOnFail makes a failed login reply include the supplied secret in
// its Message, to test that clients scrub it.
func (s *Server) EchoSecretOnFail(v bool) {
	s.mu.Lock()
	s.echoSecret = v
	s.mu.Unlock()
}

// Requests returns a copy of every request received so far (including Login).
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Count returns how many requests with the given action name were received.
func (s *Server) Count(action string) int {
	n := 0
	for _, r := range s.Requests() {
		if strings.EqualFold(r.Action, action) {
			n++
		}
	}
	return n
}

// LoginEvents returns the Events header of each successful login.
func (s *Server) LoginEvents() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.loginEvents...)
}

// Sessions returns the currently open sessions.
func (s *Server) Sessions() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]*Session(nil), s.sessions...)
}

// WaitSessions waits until at least n sessions are open.
func (s *Server) WaitSessions(n int) {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(s.Sessions()) >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.t.Fatalf("amitest: timed out waiting for %d sessions (have %d)", n, len(s.Sessions()))
}

// PushEvent sends an event to every open (logged-in or not) session.
// kv is alternating key, value strings.
func (s *Server) PushEvent(name string, kv ...string) {
	text := Event(name, kv...)
	for _, ss := range s.Sessions() {
		ss.Write(text)
	}
}

// DropConnections closes every open session without a Logoff.
func (s *Server) DropConnections() {
	for _, ss := range s.Sessions() {
		ss.Close()
	}
}

// Stop closes the listener and all sessions; Restart listens again on the
// same address.
func (s *Server) Stop() {
	s.mu.Lock()
	ln := s.ln
	s.ln = nil
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
	}
	s.DropConnections()
}

// Restart listens again on the original address after Stop.
func (s *Server) Restart() {
	s.t.Helper()
	var ln net.Listener
	var err error
	for i := 0; i < 50; i++ {
		ln, err = net.Listen("tcp", s.Addr)
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		s.t.Fatalf("amitest: relisten: %v", err)
	}
	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()
	s.wg.Add(1)
	go s.acceptLoop(ln)
}

// Close shuts the server down.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	s.Stop()
	s.wg.Wait()
}

func (s *Server) acceptLoop(ln net.Listener) {
	defer s.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		ss := &Session{srv: s, conn: c}
		s.mu.Lock()
		s.sessions = append(s.sessions, ss)
		s.mu.Unlock()
		s.wg.Add(1)
		go s.serve(ss)
	}
}

func (s *Server) removeSession(ss *Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, x := range s.sessions {
		if x == ss {
			s.sessions = append(s.sessions[:i], s.sessions[i+1:]...)
			return
		}
	}
}

func (s *Server) serve(ss *Session) {
	defer s.wg.Done()
	defer s.removeSession(ss)
	defer ss.conn.Close()
	s.mu.Lock()
	banner := s.banner
	s.mu.Unlock()
	if banner == "" {
		banner = "Asterisk Call Manager/9.0.0"
	}
	ss.Write(banner + "\r\n")
	r := bufio.NewReader(ss.conn)
	authed := false
	for {
		req, err := readRequest(r)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.requests = append(s.requests, *req)
		h := s.handlers[strings.ToLower(req.Action)]
		ignorePing, echo := s.ignorePing, s.echoSecret
		s.mu.Unlock()

		switch strings.ToLower(req.Action) {
		case "login":
			if req.Get("Username") == s.Username && req.Get("Secret") == s.Secret {
				authed = true
				s.mu.Lock()
				s.loginEvents = append(s.loginEvents, req.Get("Events"))
				s.mu.Unlock()
				ss.Write(Success(req, "Message", "Authentication accepted"))
				continue
			}
			msg := "Authentication failed"
			if echo {
				msg += " for secret " + req.Get("Secret")
			}
			ss.Write(Error(req, msg))
			return
		case "logoff":
			ss.Write(Reply(req, "Goodbye", "Message", "Thanks for all the fish."))
			return
		case "ping":
			if ignorePing {
				continue
			}
			if h == nil {
				ss.Write(Reply(req, "Success", "Ping", "Pong"))
				continue
			}
		}
		if !authed {
			ss.Write(Error(req, "Permission denied"))
			continue
		}
		if h == nil {
			ss.Write(Error(req, "Invalid/unknown command: "+req.Action))
			continue
		}
		if out := h(ss, req); out != "" {
			ss.Write(out)
		}
	}
}

func readRequest(r *bufio.Reader) (*Request, error) {
	req := &Request{}
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if len(req.Fields) == 0 {
				continue
			}
			return req, nil
		}
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		v = strings.TrimPrefix(v, " ")
		req.Fields = append(req.Fields, [2]string{k, v})
		switch strings.ToLower(k) {
		case "action":
			req.Action = v
		case "actionid":
			req.ActionID = v
		}
	}
}

func kvText(kv []string) string {
	var sb strings.Builder
	for i := 0; i+1 < len(kv); i += 2 {
		fmt.Fprintf(&sb, "%s: %s\r\n", kv[i], kv[i+1])
	}
	return sb.String()
}

// Reply builds "Response: <resp>" plus ActionID and extra key/values.
func Reply(req *Request, resp string, kv ...string) string {
	s := "Response: " + resp + "\r\n"
	if req != nil && req.ActionID != "" {
		s += "ActionID: " + req.ActionID + "\r\n"
	}
	return s + kvText(kv) + "\r\n"
}

// Success builds a "Response: Success" reply.
func Success(req *Request, kv ...string) string { return Reply(req, "Success", kv...) }

// Error builds a "Response: Error" reply with a Message.
func Error(req *Request, msg string) string { return Reply(req, "Error", "Message", msg) }

// Event builds an event message.
func Event(name string, kv ...string) string {
	return "Event: " + name + "\r\n" + kvText(kv) + "\r\n"
}

// CommandOutput builds an Asterisk 20 style Command reply (Success + Output
// lines).
func CommandOutput(req *Request, lines ...string) string {
	kv := []string{}
	for _, l := range lines {
		kv = append(kv, "Output", l)
	}
	return Reply(req, "Success", append([]string{"Message", "Command output follows"}, kv...)...)
}

// CommandFollows builds a legacy Command reply (Response: Follows, raw text,
// --END COMMAND--).
func CommandFollows(req *Request, lines ...string) string {
	s := "Response: Follows\r\nPrivilege: Command\r\n"
	if req != nil && req.ActionID != "" {
		s += "ActionID: " + req.ActionID + "\r\n"
	}
	for _, l := range lines {
		s += l + "\r\n"
	}
	return s + "--END COMMAND--\r\n\r\n"
}

// Raw returns text followed by the blank-line terminator, with no Response
// header and no ActionID (like RptLocalNodes). Lines are joined with CRLF.
func Raw(lines ...string) string { return strings.Join(lines, "\r\n") + "\r\n\r\n" }

// HandleCommands routes "Command" actions by exact command text to handlers
// of the given style. Unknown commands get "No such command".
func (s *Server) HandleCommands(cmds map[string][]string, follows bool) {
	s.Handle("Command", func(_ *Session, req *Request) string {
		lines, ok := cmds[req.Get("Command")]
		if !ok {
			lines = []string{"No such command '" + req.Get("Command") + "'"}
		}
		if follows {
			return CommandFollows(req, lines...)
		}
		return CommandOutput(req, lines...)
	})
}
