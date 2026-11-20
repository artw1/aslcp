package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Browser terminal: tickets, limits and the registry of open sessions
// (docs/PLAN-1.3.0.md section 8). The relay itself is in
// handlers_system_terminal.go.

// Defaults of the terminal limits (decided: docs/PLAN-1.3.0.md 8.2 and 15).
const (
	terminalTicketTTL = 30 * time.Second
	terminalIdle      = 15 * time.Minute
	terminalMaxLen    = 8 * time.Hour
	terminalMaxLive   = 2
	// maxTickets bounds the unredeemed tickets kept in memory.
	maxTickets = 64

	settingTerminal = "terminal.enabled"
)

// ticketStore holds one-time WebSocket tickets. A browser cannot add the
// CSRF header to a WebSocket handshake, so the page first POSTs (with the
// header) for a ticket and puts it in the WebSocket URL. A ticket is random,
// valid for ttl, usable once, and tied to the user and the login session it
// was issued to. The zero value is ready to use.
type ticketStore struct {
	mu  sync.Mutex
	m   map[string]ticketInfo
	ttl time.Duration
	now func() time.Time
}

type ticketInfo struct {
	user    int64
	session string // hash of the session token
	target  string // id of the detached session to reattach to ("": a new one)
	expires time.Time
}

var errTooManyTickets = errors.New("too many open tickets")

func (t *ticketStore) clock() time.Time {
	t.mu.Lock()
	now := t.now
	t.mu.Unlock()
	if now != nil {
		return now()
	}
	return time.Now()
}

func (t *ticketStore) lifetime() time.Duration {
	if t.ttl > 0 {
		return t.ttl
	}
	return terminalTicketTTL
}

// ticketKey is what is stored: a ticket is only ever held as its hash.
func ticketKey(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// issue returns a new ticket for user and session.
func (t *ticketStore) issue(user int64, session string) (string, error) {
	return t.issueFor(user, session, "")
}

// issueFor is issue for reattaching to the session target.
func (t *ticketStore) issueFor(user int64, session, target string) (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(b)
	now := t.clock()
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.m == nil {
		t.m = map[string]ticketInfo{}
	}
	for k, v := range t.m {
		if !now.Before(v.expires) {
			delete(t.m, k)
		}
	}
	if len(t.m) >= maxTickets {
		return "", errTooManyTickets
	}
	t.m[ticketKey(tok)] = ticketInfo{user: user, session: session, target: target, expires: now.Add(t.lifetime())}
	return tok, nil
}

// redeem consumes the ticket. It is removed whether or not it matches, so a
// wrong guess burns it; it succeeds only before it expires and only for the
// user and session it was issued to.
func (t *ticketStore) redeem(tok string, user int64, session string) bool {
	_, ok := t.redeemFor(tok, user, session)
	return ok
}

// redeemFor is redeem and also returns the session the ticket is for ("" for
// a new one).
func (t *ticketStore) redeemFor(tok string, user int64, session string) (string, bool) {
	if tok == "" {
		return "", false
	}
	k := ticketKey(tok)
	t.mu.Lock()
	v, ok := t.m[k]
	delete(t.m, k)
	t.mu.Unlock()
	if !ok || !t.clock().Before(v.expires) {
		return "", false
	}
	if v.user != user || subtle.ConstantTimeCompare([]byte(v.session), []byte(session)) != 1 {
		return "", false
	}
	return v.target, true
}

// terminalExpiry says whether a session has to end now: "idle" when no
// keyboard input arrived for idle, "limit" at the hard maximum length, else
// "". Resize messages and program output do not count as activity.
func terminalExpiry(now, started, lastInput time.Time, idle, maxLen time.Duration) string {
	if maxLen > 0 && !now.Before(started.Add(maxLen)) {
		return "limit"
	}
	if idle > 0 && !now.Before(lastInput.Add(idle)) {
		return "idle"
	}
	return ""
}

// termSession is one open terminal as the registry sees it.
type termSession struct {
	started   time.Time
	owner     int64
	id        string       // the helper's session id, once known
	lastInput atomic.Int64 // unix nanoseconds of the last keyboard input
	reason    atomic.Value // string: why it ended, set once by end
	cancel    func()
	mu        sync.Mutex
	linuxUser string
}

// end records why the session ends (first call wins) and stops it.
func (s *termSession) end(reason string) {
	s.reason.CompareAndSwap(nil, reason)
	s.cancel()
}

func (s *termSession) endReason() string {
	if r, ok := s.reason.Load().(string); ok {
		return r
	}
	return "closed"
}

func (s *termSession) setID(id string) {
	s.mu.Lock()
	s.id = id
	s.mu.Unlock()
}

func (s *termSession) sessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.id
}

// detachReasons are the end reasons that mean the transport was lost rather
// than anybody ending the session: the session is detached, not stopped.
func isDetachReason(r string) bool { return r == "detach" || r == "error" }

func (s *termSession) setLinuxUser(u string) {
	s.mu.Lock()
	s.linuxUser = u
	s.mu.Unlock()
}

func (s *termSession) user() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.linuxUser
}

// termState is the terminal state of a Server. The zero value is ready to use;
// tests may set the limits before the first request.
type termState struct {
	tickets ticketStore

	mu   sync.Mutex
	live map[*termSession]struct{}

	cfgMu   sync.RWMutex  // guards the settings below (tests change them)
	maxLive int           // 0: terminalMaxLive
	idle    time.Duration // 0: terminalIdle
	maxLen  time.Duration // 0: terminalMaxLen
	tick    time.Duration // watcher interval, 0: 1 second
	now     func() time.Time
}

func (t *termState) limits() (maxLive int, idle, maxLen time.Duration) {
	t.cfgMu.RLock()
	maxLive, idle, maxLen = t.maxLive, t.idle, t.maxLen
	t.cfgMu.RUnlock()
	if maxLive == 0 {
		maxLive = terminalMaxLive
	}
	if idle == 0 {
		idle = terminalIdle
	}
	if maxLen == 0 {
		maxLen = terminalMaxLen
	}
	return
}

func (t *termState) clock() time.Time {
	t.cfgMu.RLock()
	now := t.now
	t.cfgMu.RUnlock()
	if now != nil {
		return now()
	}
	return time.Now()
}

func (t *termState) interval() time.Duration {
	t.cfgMu.RLock()
	defer t.cfgMu.RUnlock()
	if t.tick > 0 {
		return t.tick
	}
	return time.Second
}

// reserve registers a session unless the limit of concurrent sessions is
// reached; extra counts sessions the helper holds that are not attached (they
// count against the limit too).
func (t *termState) reserve(cancel func(), owner int64, extra int) (*termSession, bool) {
	maxLive, _, _ := t.limits()
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.live)+extra >= maxLive {
		return nil, false
	}
	if t.live == nil {
		t.live = map[*termSession]struct{}{}
	}
	now := t.clock()
	s := &termSession{started: now, owner: owner, cancel: cancel}
	s.lastInput.Store(now.UnixNano())
	t.live[s] = struct{}{}
	return s, true
}

func (t *termState) release(s *termSession) {
	t.mu.Lock()
	delete(t.live, s)
	t.mu.Unlock()
}

// liveByID returns the attached session with the helper's id, or nil.
func (t *termState) liveByID(id string) *termSession {
	t.mu.Lock()
	defer t.mu.Unlock()
	for s := range t.live {
		if s.sessionID() == id {
			return s
		}
	}
	return nil
}

func (t *termState) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.live)
}

// endAll ends every open session with reason (the feature was switched off).
func (t *termState) endAll(reason string) {
	t.mu.Lock()
	var all []*termSession
	for s := range t.live {
		all = append(all, s)
	}
	t.mu.Unlock()
	for _, s := range all {
		s.end(reason)
	}
}

// terminalOriginOK is the Origin check of the WebSocket upgrade: the header
// must be present (browsers always send it on a handshake) and name this
// host or one of the extra allowed origins, as for the CSRF check of other
// writes. The comparison is the one of auth.CheckCSRF, minus its allowance
// for a missing header.
func terminalOriginOK(r *http.Request, extra []string) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	for _, o := range extra {
		if origin == o {
			return true
		}
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, r.Host)
}

// cspHost returns host if it is safe to put in a Content-Security-Policy
// source list (letters, digits, dot, hyphen, colon, brackets), else "".
func cspHost(host string) string {
	if host == "" || len(host) > 255 {
		return ""
	}
	for _, c := range host {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '.', c == '-', c == ':', c == '[', c == ']':
		default:
			return ""
		}
	}
	return host
}
