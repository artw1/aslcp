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

	"github.com/artw1/aslcp/internal/rpt"
	"github.com/artw1/aslcp/internal/store"
)

// Store is what the service needs from the database.
type Store interface {
	SettingsStore
	ListActivity(nodes []string, limit int) ([]store.Activity, error)
}

// Options configure a Service.
type Options struct {
	Store          Store
	Lookup         Lookup
	Logger         *log.Logger
	DefaultEnabled bool // initial value of "enabled" when never set (demo mode)
	// OnClients is called with the number of connected SSE clients whenever
	// it changes (the monitor polls fast while anyone watches).
	OnClients func(n int)
	// Bridge returns the private digital modes bridge node and its display name,
	// or "" when there is none. Optional.
	Bridge func() (node, name string)
	// DV returns the digital modes calls (Now calling and heard), already
	// resolved from local caches. Optional; only read while Settings.ShowDV is on.
	DV  func() DVInput
	Now func() time.Time
}

// Service serves the public status data.
type Service struct {
	opt Options
	cfg atomic.Pointer[Settings]
	mon atomic.Pointer[rpt.Monitor]
	lim *limiter
	hub *hub

	dvMu    sync.Mutex
	dvLast  time.Time
	dvTimer *time.Timer
	dvSent  []byte

	// Limits and timing; set before the service is used (tests change them).
	MaxPerIP     int           // SSE connections per client address (default 4)
	MaxClients   int           // SSE connections in total (default 200)
	PingInterval time.Duration // default 20s
	MaxStream    time.Duration // an SSE stream is closed after this long (default 6h)
	WriteTimeout time.Duration // per write on an SSE stream (default 10s)
	DVInterval   time.Duration // shortest time between two "dv" events (default 1s)
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
		MaxPerIP: 4, MaxClients: 200, PingInterval: 20 * time.Second, MaxStream: 6 * time.Hour, WriteTimeout: 10 * time.Second, DVInterval: dvInterval,
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

// SetMonitor tells the service where node status comes from.
func (s *Service) SetMonitor(m *rpt.Monitor) { s.mon.Store(m) }

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
func (s *Service) Close() { s.stopDV(); s.hub.closeAll() }

func (s *Service) selected(cfg *Settings) map[string]bool {
	if len(cfg.Nodes) == 0 {
		return nil
	}
	m := map[string]bool{}
	for _, n := range cfg.Nodes {
		m[n] = true
	}
	return m
}

// nodeIDs returns the shown local nodes in monitor order.
func (s *Service) nodeIDs(cfg *Settings, m *rpt.Monitor) []string {
	if m == nil {
		return nil
	}
	sel := s.selected(cfg)
	var out []string
	for _, n := range m.Nodes() {
		if sel == nil || sel[n] {
			out = append(out, n)
		}
	}
	return out
}

// Snapshot builds the whole page state under the current settings.
func (s *Service) Snapshot() Snapshot { return s.build(true) }

func (s *Service) build(withActivity bool) Snapshot {
	cfg := s.cfg.Load()
	now := s.opt.Now()
	v := s.view(cfg, now)
	snap := Snapshot{Now: now.Unix(), Callsigns: cfg.ShowCallsigns, ShowNetwork: cfg.ShowNetwork, ShowActivity: cfg.ShowActivity, ShowDV: cfg.ShowDV, Nodes: []Node{}}
	m := s.mon.Load()
	ids := s.nodeIDs(cfg, m)
	if m != nil {
		all := m.Snapshot()
		for _, id := range ids {
			if st, ok := all[id]; ok {
				snap.Nodes = append(snap.Nodes, BuildNode(st, s.opt.Lookup, v, true))
			}
		}
	}
	snap.Title = cfg.Title
	if snap.Title == "" {
		snap.Title = "AllStarLink node status"
		if len(snap.Nodes) > 0 {
			if in := snap.Nodes[0].Info; in != nil && in.Callsign != "" {
				snap.Title = in.Callsign + " AllStarLink node " + snap.Nodes[0].Node
			}
		}
	}
	snap.Title = scrub(snap.Title)
	if cfg.ShowActivity && withActivity {
		snap.Activity = s.activity(cfg, ids, v)
	}
	if withActivity {
		snap.DV = s.dv(cfg, v)
	}
	return snap
}

func (s *Service) activity(cfg *Settings, ids []string, v View) []Activity {
	if len(ids) == 0 || s.opt.Store == nil {
		return []Activity{}
	}
	rows, err := s.opt.Store.ListActivity(ids, ActivityLimit)
	if err != nil {
		s.opt.Logger.Printf("public: activity: %v", err)
		return []Activity{}
	}
	return BuildActivity(rows, s.opt.Lookup, v)
}

// HandleUpdate forwards a monitor update to the connected clients.
func (s *Service) HandleUpdate(u rpt.Update) {
	if s.hub.count() == 0 {
		return
	}
	cfg := s.cfg.Load()
	if !cfg.Enabled {
		return
	}
	c := u.Changes
	if c.Added || c.Removed {
		s.hub.broadcast(frame("snapshot", s.Snapshot()))
		return
	}
	if sel := s.selected(cfg); sel != nil && !sel[u.Node] {
		return
	}
	if c.Resync || c.Recovered || c.Stale || c.Initial {
		s.hub.broadcast(frame("snapshot", s.Snapshot()))
		return
	}
	v := s.view(cfg, s.opt.Now())
	s.hub.broadcast(frame("node", BuildNode(u.Status, s.opt.Lookup, v, false)))
	if c.Network && cfg.ShowNetwork {
		s.hub.broadcast(frame("network", NetworkEvent{Node: scrubNode(u.Node), Network: BuildNetwork(u.Status, s.opt.Lookup, v)}))
	}
}

// ActivityRecorded tells the service a transmission was stored.
func (s *Service) ActivityRecorded(a store.Activity) {
	cfg := s.cfg.Load()
	if s.hub.count() == 0 || !cfg.Enabled || !cfg.ShowActivity {
		return
	}
	if sel := s.selected(cfg); sel != nil && !sel[a.Node] {
		return
	}
	now := s.opt.Now()
	v := s.view(cfg, now)
	s.hub.broadcast(frame("activity", ActivityEvent{Now: now.Unix(), Activity: s.activity(cfg, s.nodeIDs(cfg, s.mon.Load()), v)}))
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
		out.Title = s.build(false).Title
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

// view is the build configuration for the current settings.
func (s *Service) view(cfg *Settings, now time.Time) View {
	v := View{Callsigns: cfg.ShowCallsigns, Network: cfg.ShowNetwork, Now: now}
	if s.opt.Bridge != nil {
		v.Bridge, v.BridgeName = s.opt.Bridge()
	}
	return v
}
