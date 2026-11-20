package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/artw1/aslcp/internal/auth"
	"github.com/artw1/aslcp/internal/helper"
	"github.com/artw1/aslcp/internal/store"
)

// TerminalOpener is implemented by the helper client (and by the demo
// helper) next to SystemHost: the API finds it by type assertion on
// Deps.SysHost, so a helper without it answers 503. The stream carries the
// frames of helper/terminal_frame.go; closing it hangs the session up.
type TerminalOpener interface {
	OpenTerminal(ctx context.Context, rows, cols uint16) (io.ReadWriteCloser, error)
}

func (s *Server) systemTerminalRoutes() {
	m := s.mux
	m.HandleFunc("GET /api/system/terminal", s.authed(store.RoleAdmin, s.handleTerminalGet))
	m.HandleFunc("PUT /api/system/terminal", s.authed(store.RoleAdmin, s.handleTerminalPut))
	m.HandleFunc("POST /api/system/terminal/ticket", s.authed(store.RoleAdmin, s.handleTerminalTicket))
	m.HandleFunc("GET /api/system/terminal/ws", s.authed(store.RoleAdmin, s.handleTerminalWS))
}

// terminalEnabled is the admin switch (default off).
func (s *Server) terminalEnabled() bool {
	if s.d.Store == nil {
		return false
	}
	v, err := s.d.Store.GetSetting(settingTerminal)
	return err == nil && v == "true"
}

// terminalOpener returns the helper's terminal support or answers 503.
func (s *Server) terminalOpener(w http.ResponseWriter) TerminalOpener {
	if s.sysHost(w) == nil {
		return nil
	}
	op, ok := s.d.SysHost.(TerminalOpener)
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "system_unavailable", "not available: the helper has no terminal support")
		return nil
	}
	return op
}

// TerminalStatus is the answer of GET and PUT /api/system/terminal.
type TerminalStatus struct {
	Enabled bool `json:"enabled"`
	// Available: a helper with terminal support is wired in.
	Available   bool `json:"available"`
	Sessions    int  `json:"sessions"`
	MaxSessions int  `json:"maxSessions"`
	IdleMinutes int  `json:"idleMinutes"`
	MaxHours    int  `json:"maxHours"`
}

func (s *Server) terminalStatus() TerminalStatus {
	maxLive, idle, maxLen := s.term.limits()
	_, avail := s.d.SysHost.(TerminalOpener)
	return TerminalStatus{
		Enabled: s.terminalEnabled(), Available: s.d.SysHost != nil && avail,
		Sessions: s.term.count(), MaxSessions: maxLive,
		IdleMinutes: int(idle / time.Minute), MaxHours: int(maxLen / time.Hour),
	}
}

func (s *Server) handleTerminalGet(w http.ResponseWriter, _ *http.Request, _ store.User) {
	writeJSON(w, 200, s.terminalStatus())
}

func (s *Server) handleTerminalPut(w http.ResponseWriter, r *http.Request, u store.User) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decode(w, r, &body, false) {
		return
	}
	if body.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "bad_request", "nothing to change")
		return
	}
	if s.d.Store == nil {
		writeErr(w, http.StatusServiceUnavailable, "system_unavailable", "not available")
		return
	}
	before := s.terminalEnabled()
	if before != *body.Enabled {
		v := "false"
		if *body.Enabled {
			v = "true"
		}
		if err := s.d.Store.SetSetting(settingTerminal, v); err != nil {
			s.internal(w, err)
			return
		}
		s.d.Logger.Printf("terminal %s by %s", map[bool]string{true: "enabled", false: "disabled"}[*body.Enabled], u.Username)
		s.audit(u, "system.terminal", map[string]any{"from": before, "to": *body.Enabled})
	}
	if !*body.Enabled {
		s.term.endAll("disabled")
	}
	writeJSON(w, 200, s.terminalStatus())
}

func (s *Server) handleTerminalTicket(w http.ResponseWriter, r *http.Request, u store.User) {
	if s.terminalOpener(w) == nil {
		return
	}
	if !s.terminalEnabled() {
		writeErr(w, http.StatusForbidden, "terminal_disabled", "the terminal is turned off")
		return
	}
	if maxLive, _, _ := s.term.limits(); s.term.count() >= maxLive {
		writeErr(w, http.StatusTooManyRequests, "too_many_sessions", "too many terminal sessions are open")
		return
	}
	tok, err := s.term.tickets.issue(u.ID, auth.SessionHash(auth.TokenFrom(r)))
	if err != nil {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", "too many tickets, try again in a moment")
		return
	}
	writeJSON(w, 200, map[string]any{"ticket": tok, "expiresIn": int(terminalTicketTTL / time.Second)})
}

// queryDim reads rows or cols from the query: absent or invalid is 0 (the
// default size), a value above the maximum is cut.
func queryDim(r *http.Request, name string) uint16 {
	n, err := strconv.Atoi(r.URL.Query().Get(name))
	if err != nil || n < 1 {
		return 0
	}
	if n > helper.MaxRows {
		n = helper.MaxRows
	}
	return uint16(n)
}

// wsResize is the one text message the browser may send.
type wsResize struct {
	Type string `json:"type"`
	Rows int    `json:"rows"`
	Cols int    `json:"cols"`
}

func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request, u store.User) {
	// Cross-site WebSocket hijacking is the main risk: the Origin must name
	// this host, whatever the cookie says.
	if !terminalOriginOK(r, s.d.AllowedOrigins) {
		writeErr(w, http.StatusForbidden, "csrf", "cross-origin request rejected")
		return
	}
	op := s.terminalOpener(w)
	if op == nil {
		return
	}
	if !s.terminalEnabled() {
		writeErr(w, http.StatusForbidden, "terminal_disabled", "the terminal is turned off")
		return
	}
	if !s.term.tickets.redeem(r.URL.Query().Get("ticket"), u.ID, auth.SessionHash(auth.TokenFrom(r))) {
		writeErr(w, http.StatusForbidden, "bad_ticket", "invalid or expired ticket")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sess, ok := s.term.reserve(cancel)
	if !ok {
		writeErr(w, http.StatusTooManyRequests, "too_many_sessions", "too many terminal sessions are open")
		return
	}
	defer s.term.release(sess)

	rows, cols := queryDim(r, "rows"), queryDim(r, "cols")
	stream, err := op.OpenTerminal(r.Context(), rows, cols)
	if err != nil {
		s.systemError(w, err)
		return
	}
	defer stream.Close() // the helper hangs the session up when this closes

	// The Origin was checked above with the panel's own rule; the library's
	// default (same host only) would refuse the allowed extra origins.
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true, CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		s.d.Logger.Printf("terminal: websocket upgrade: %v", err)
		return
	}
	ws.SetReadLimit(64 << 10)

	ip := auth.ClientIP(r)
	s.d.Logger.Printf("terminal session started by %s", u.Username)
	s.audit(u, "system.terminal.start", map[string]any{"ip": ip})
	s.relayTerminal(ctx, ws, stream, sess, r)
	reason := sess.endReason()
	d := s.term.clock().Sub(sess.started)
	linux := sess.user()
	if linux == "" {
		linux = "unknown"
	}
	s.d.Logger.Printf("terminal session of %s ended (%s)", u.Username, reason)
	s.audit(u, "system.terminal.end", map[string]any{"seconds": int(d / time.Second), "reason": reason, "linuxUser": linux, "ip": ip})
}

// cleanLinuxUser accepts a plausible user name from the helper and nothing else.
func cleanLinuxUser(b []byte) string {
	if len(b) == 0 || len(b) > 64 {
		return ""
	}
	for _, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '_', c == '-', c == '$':
		default:
			return ""
		}
	}
	return string(b)
}

// relayTerminal moves bytes between the WebSocket and the helper stream until
// the session ends for any reason. It never inspects or stores the bytes.
//
// Browser to helper: binary messages are keyboard input (they reset the idle
// timer); text messages are JSON control, only {"type":"resize","rows","cols"}.
// Helper to browser: data frames become binary messages; a user frame names
// the Linux user for the audit record.
func (s *Server) relayTerminal(ctx context.Context, ws *websocket.Conn, stream io.ReadWriteCloser, sess *termSession, req *http.Request) {
	_, idle, maxLen := s.term.limits()
	var wg sync.WaitGroup

	// Browser to helper.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sess.end("closed")
		for {
			typ, data, err := ws.Read(context.Background()) // not ctx: the close frame needs a reader
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageBinary:
				sess.lastInput.Store(s.term.clock().UnixNano())
				if err := helper.WriteData(stream, data); err != nil {
					sess.end("error")
					return
				}
			case websocket.MessageText:
				var m wsResize
				if json.Unmarshal(data, &m) != nil || m.Type != "resize" || m.Rows < 1 || m.Cols < 1 || m.Rows > helper.MaxRows || m.Cols > helper.MaxCols {
					continue
				}
				if err := helper.WriteResize(stream, uint16(m.Rows), uint16(m.Cols)); err != nil {
					sess.end("error")
					return
				}
			}
		}
	}()

	// Helper to browser.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer sess.end("closed")
		for {
			typ, p, err := helper.ReadFrame(stream)
			if err != nil {
				return
			}
			switch typ {
			case helper.FrameData:
				wctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				err := ws.Write(wctx, websocket.MessageBinary, p)
				cancel()
				if err != nil {
					return
				}
			case helper.FrameUser:
				sess.setLinuxUser(cleanLinuxUser(p))
			}
		}
	}()

	// Limits: idle, maximum length, login session, the switch.
	wg.Add(1)
	go func() {
		defer wg.Done()
		t := time.NewTicker(s.term.interval())
		defer t.Stop()
		lastAuth := time.Now()
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stop:
				sess.end("closed")
				return
			case <-t.C:
			}
			if reason := terminalExpiry(s.term.clock(), sess.started, time.Unix(0, sess.lastInput.Load()), idle, maxLen); reason != "" {
				sess.end(reason)
				return
			}
			if !s.terminalEnabled() {
				sess.end("disabled")
				return
			}
			if time.Since(lastAuth) >= 5*time.Second {
				lastAuth = time.Now()
				if u, ok := s.d.Auth.Authenticate(req); !ok || auth.RoleRank(u.Role) < auth.RoleRank(store.RoleAdmin) {
					sess.end("logout")
					return
				}
			}
		}
	}()

	<-ctx.Done()
	_ = stream.Close() // unblocks the helper reader; the helper kills the session
	_ = ws.Close(websocket.StatusNormalClosure, sess.endReason())
	wg.Wait()
}

// SetTerminalLimits overrides the terminal limits (tests): concurrent
// sessions, idle timeout, maximum length, the interval of the limit watcher
// and the clock. Zero values keep the defaults. Call it before serving.
func (s *Server) SetTerminalLimits(maxLive int, idle, maxLen, tick time.Duration, now func() time.Time) {
	s.term.cfgMu.Lock()
	s.term.maxLive, s.term.idle, s.term.maxLen, s.term.tick, s.term.now = maxLive, idle, maxLen, tick, now
	s.term.cfgMu.Unlock()
	s.term.tickets.mu.Lock()
	s.term.tickets.now = now
	s.term.tickets.mu.Unlock()
}
