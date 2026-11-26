package public

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Options configure a Service.
type Options struct {
	Store          SettingsStore
	Logger         *log.Logger
	DefaultEnabled bool // initial value of "enabled" when never set (demo mode)
	// OnClients is called with the number of connected SSE clients whenever
	// it changes.
	OnClients func(n int)
	// Input returns the node's state and calls, already resolved from local
	// caches (the service never looks anything up itself, so a request of a
	// visitor can never cause a lookup). Optional.
	Input func() Input
	Now   func() time.Time
}

// Service serves the public status data.
type Service struct {
	opt Options
	cfg atomic.Pointer[Settings]
	lim *limiter
	hub *hub

	chMu    sync.Mutex
	chLast  time.Time
	chTimer *time.Timer
	chSent  []byte

	// Limits and timing; set before the service is used (tests change them).
	MaxPerIP     int           // SSE connections per client address (default 4)
	MaxClients   int           // SSE connections in total (default 200)
	PingInterval time.Duration // default 20s
	MaxStream    time.Duration // an SSE stream is closed after this long (default 6h)
	WriteTimeout time.Duration // per write on an SSE stream (default 10s)
	Interval     time.Duration // shortest time between two "snapshot" events (default 1s)
}

// New returns a Service with the persisted settings loaded.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = log.Default()
	}
	s := &Service{
		opt: o, lim: newLimiter(2, 10),
		MaxPerIP: 4, MaxClients: 200, PingInterval: 20 * time.Second, MaxStream: 6 * time.Hour, WriteTimeout: 10 * time.Second, Interval: time.Second,
	}
	s.hub = newHub(s)
	st := LoadSettings(o.Store, o.DefaultEnabled)
	s.cfg.Store(&st)
	return s
}

// Settings returns the current settings.
func (s *Service) Settings() Settings { return *s.cfg.Load() }

// Enabled reports whether the public view is on.
func (s *Service) Enabled() bool { return s.cfg.Load().Enabled }

// Clients returns the number of connected SSE clients.
func (s *Service) Clients() int { return s.hub.count() }

// SetRateLimit changes the per-address request rate limit (requests per
// second, burst).
func (s *Service) SetRateLimit(rate float64, burst int) { s.lim.set(rate, burst) }

// Update validates, persists and applies new settings, and refreshes the
// connected clients (so a changed selection or visibility applies live).
func (s *Service) Update(ns Settings) error {
	if err := ns.Validate(); err != nil {
		return err
	}
	if err := SaveSettings(s.opt.Store, ns); err != nil {
		return err
	}
	s.cfg.Store(&ns)
	if !ns.Enabled {
		s.hub.closeAll()
		return nil
	}
	s.hub.broadcast(frame("snapshot", s.Snapshot()))
	return nil
}

// Close ends all SSE streams.
func (s *Service) Close() { s.stopTimer(); s.hub.closeAll() }

// Snapshot builds the whole page state under the current settings.
func (s *Service) Snapshot() Snapshot {
	var in Input
	if s.opt.Input != nil {
		in = s.opt.Input()
	}
	return Build(in, *s.cfg.Load(), s.opt.Now())
}

// Changed tells the service that the node's state or calls changed.
// Updates are coalesced: at most one "snapshot" event per Interval goes to
// the clients, only when it differs from the last one, and nothing at all
// while the page is off or nobody is watching.
func (s *Service) Changed() {
	if !s.cfg.Load().Enabled || s.hub.count() == 0 {
		return
	}
	s.chMu.Lock()
	if s.chTimer != nil { // one is scheduled already and will read the newest data
		s.chMu.Unlock()
		return
	}
	now := s.opt.Now()
	wait := s.Interval - now.Sub(s.chLast)
	if wait <= 0 {
		s.chLast = now
		s.chMu.Unlock()
		s.send()
		return
	}
	s.chTimer = time.AfterFunc(wait, func() {
		s.chMu.Lock()
		s.chTimer = nil
		s.chLast = s.opt.Now()
		s.chMu.Unlock()
		s.send()
	})
	s.chMu.Unlock()
}

func (s *Service) send() {
	if !s.cfg.Load().Enabled || s.hub.count() == 0 {
		return
	}
	sn := s.Snapshot()
	sn.Now = 0 // compared without the clock
	cmp := frame("snapshot", sn)
	s.chMu.Lock()
	same := string(cmp) == string(s.chSent)
	s.chSent = cmp
	s.chMu.Unlock()
	if !same {
		s.hub.broadcast(frame("snapshot", s.Snapshot()))
	}
}

func (s *Service) stopTimer() {
	s.chMu.Lock()
	if s.chTimer != nil {
		s.chTimer.Stop()
		s.chTimer = nil
	}
	s.chMu.Unlock()
}

func frame(event string, v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		b = []byte("null")
	}
	return []byte(fmt.Sprintf("event: %s\ndata: %s\n\n", event, b))
}

// ---------------------------------------------------------------- HTTP

// Info is the response of GET /api/public/info: what the app needs to decide
// between the login page and the public view.
type InfoResponse struct {
	Enabled bool   `json:"enabled"`
	Title   string `json:"title"`
	// LoggedIn: the request carries a valid session (so the app can skip the
	// session check, which would be a 401 for visitors).
	LoggedIn bool `json:"loggedIn"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		status, b = 500, []byte(`{"error":"encoding error","code":"internal"}`)
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(append(b, '\n'))
}

func errJSON(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]string{"error": msg, "code": code})
}

// ClientKey returns the rate-limit / connection-cap key of a request: the
// remote address, or (only with trustProxy) the rightmost X-Forwarded-For
// entry, which is the one added by the trusted proxy. IPv6 addresses are
// grouped by /64.
func ClientKey(r *http.Request, trustProxy bool) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if ip := net.ParseIP(strings.TrimSpace(parts[len(parts)-1])); ip != nil {
				host = ip.String()
			}
		}
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if ip4 := ip.To4(); ip4 != nil {
		return ip4.String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}

// Info serves GET /api/public/info (always answers; no data besides the flag
// and the title).
func (s *Service) Info(w http.ResponseWriter, r *http.Request, loggedIn bool) {
	cfg := s.cfg.Load()
	if !s.lim.allow(ClientKey(r, cfg.TrustProxy), s.opt.Now()) {
		w.Header().Set("Retry-After", "1")
		errJSON(w, 429, "rate_limited", "too many requests")
		return
	}
	out := InfoResponse{Enabled: cfg.Enabled, LoggedIn: loggedIn}
	if cfg.Enabled {
		out.Title = s.Snapshot().Title
	}
	writeJSON(w, 200, out)
}

// gate answers 404 while the public view is disabled and 429 when the
// client is over its rate limit; it reports whether the request may go on.
func (s *Service) gate(w http.ResponseWriter, r *http.Request) (key string, ok bool) {
	cfg := s.cfg.Load()
	if !cfg.Enabled {
		errJSON(w, 404, "not_found", "no such endpoint")
		return "", false
	}
	key = ClientKey(r, cfg.TrustProxy)
	if !s.lim.allow(key, s.opt.Now()) {
		w.Header().Set("Retry-After", "1")
		errJSON(w, 429, "rate_limited", "too many requests")
		return "", false
	}
	return key, true
}

// Status serves GET /api/public/status.
func (s *Service) Status(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.gate(w, r); !ok {
		return
	}
	writeJSON(w, 200, s.Snapshot())
}

// Events serves GET /api/public/events: a snapshot, then only changes.
func (s *Service) Events(w http.ResponseWriter, r *http.Request) {
	key, ok := s.gate(w, r)
	if !ok {
		return
	}
	c, err := s.hub.add(key)
	if err != nil {
		w.Header().Set("Retry-After", "5")
		errJSON(w, err.status, "too_many_streams", err.msg)
		return
	}
	defer s.hub.remove(c)

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	write := func(b []byte) bool {
		_ = rc.SetWriteDeadline(s.opt.Now().Add(s.WriteTimeout))
		if _, err := w.Write(b); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	if !write(frame("snapshot", s.Snapshot())) {
		return
	}
	ping := time.NewTicker(s.PingInterval)
	defer ping.Stop()
	max := time.NewTimer(s.MaxStream)
	defer max.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-c.done:
			return
		case <-max.C:
			return
		case b := <-c.ch:
			if !write(b) {
				return
			}
		case <-ping.C:
			if !write([]byte(": ping\n\n")) {
				return
			}
		}
	}
}

// ---------------------------------------------------------------- hub

type client struct {
	ch   chan []byte
	done chan struct{}
	once sync.Once
	key  string
}

func (c *client) close() { c.once.Do(func() { close(c.done) }) }

type hubError struct {
	status int
	msg    string
}

type hub struct {
	s       *Service
	mu      sync.Mutex
	clients map[*client]struct{}
	perKey  map[string]int
}

func newHub(s *Service) *hub {
	return &hub{s: s, clients: map[*client]struct{}{}, perKey: map[string]int{}}
}

const clientBuffer = 32

func (h *hub) add(key string) (*client, *hubError) {
	h.mu.Lock()
	if h.perKey[key] >= h.s.MaxPerIP {
		h.mu.Unlock()
		return nil, &hubError{429, "too many live connections from this address"}
	}
	if len(h.clients) >= h.s.MaxClients {
		h.mu.Unlock()
		return nil, &hubError{503, "too many live connections, try again later"}
	}
	c := &client{ch: make(chan []byte, clientBuffer), done: make(chan struct{}), key: key}
	h.clients[c] = struct{}{}
	h.perKey[key]++
	n := len(h.clients)
	h.mu.Unlock()
	h.notify(n)
	return c, nil
}

func (h *hub) drop(c *client) bool {
	if _, ok := h.clients[c]; !ok {
		return false
	}
	delete(h.clients, c)
	if h.perKey[c.key]--; h.perKey[c.key] <= 0 {
		delete(h.perKey, c.key)
	}
	return true
}

func (h *hub) remove(c *client) {
	h.mu.Lock()
	ok := h.drop(c)
	n := len(h.clients)
	h.mu.Unlock()
	c.close()
	if ok {
		h.notify(n)
	}
}

func (h *hub) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.clients)
}

func (h *hub) notify(n int) {
	if h.s.opt.OnClients != nil {
		h.s.opt.OnClients(n)
	}
}

// broadcast never blocks: a client that cannot keep up is dropped (the page
// reconnects and gets a fresh snapshot).
func (h *hub) broadcast(b []byte) {
	h.mu.Lock()
	var dropped []*client
	for c := range h.clients {
		select {
		case c.ch <- b:
		default:
			h.drop(c)
			dropped = append(dropped, c)
		}
	}
	n := len(h.clients)
	h.mu.Unlock()
	for _, c := range dropped {
		c.close()
	}
	if len(dropped) > 0 {
		h.notify(n)
	}
}

func (h *hub) closeAll() {
	h.mu.Lock()
	cs := h.clients
	h.clients = map[*client]struct{}{}
	h.perKey = map[string]int{}
	h.mu.Unlock()
	for c := range cs {
		c.close()
	}
	if len(cs) > 0 {
		h.notify(0)
	}
}

// ---------------------------------------------------------------- limiter

// limiter is a per-key token bucket.
type limiter struct {
	mu      sync.Mutex
	rate    float64
	burst   float64
	buckets map[string]*bucket
	swept   time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

func newLimiter(rate float64, burst int) *limiter {
	return &limiter{rate: rate, burst: float64(burst), buckets: map[string]*bucket{}}
}

func (l *limiter) set(rate float64, burst int) {
	l.mu.Lock()
	l.rate, l.burst = rate, float64(burst)
	l.mu.Unlock()
}

func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 4096 && now.Sub(l.swept) > time.Minute {
		for k, b := range l.buckets {
			if now.Sub(b.last) > time.Minute {
				delete(l.buckets, k)
			}
		}
		l.swept = now
	}
	b := l.buckets[key]
	if b == nil {
		if len(l.buckets) > 100000 {
			return false // under attack with spread-out addresses: refuse new keys
		}
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	if d := now.Sub(b.last).Seconds(); d > 0 {
		b.tokens += d * l.rate
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
