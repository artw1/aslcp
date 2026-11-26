package register

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Defaults taken from the registration spec.
const (
	DefaultURL      = "https://register.allstarlink.org/"
	DefaultInterval = 60 * time.Second
	DefaultTimeout  = 15 * time.Second
	DefaultMaxBack  = 5 * time.Minute
	userAgent       = "asterisk-libcurl-agent/1.0"
	maxReplyBytes   = 64 << 10
)

// ErrNotRegistered means the registrar answered but did not accept the node,
// for example because of a wrong password.
var ErrNotRegistered = errors.New("register: registrar did not accept registration")

// Config configures a Registrar.
type Config struct {
	Node     string // node number
	Password string // never logged or included in errors
	Port     int    // IAX bind port to report; 0 omits it
	URL      string // registrar URL; default DefaultURL
	Interval time.Duration
	// MaxBackoff caps the retry delay after failures. Default DefaultMaxBack.
	MaxBackoff time.Duration

	// HTTPClient is injectable for tests. The default has a 15 s timeout, no
	// redirects and an IPv4-only dialer.
	HTTPClient *http.Client
	Clock      Clock
	// LookupHost resolves the node's DNS name for the self-check. If nil the
	// DNS check is skipped and DNSOK stays false but Ready ignores it.
	LookupHost func(ctx context.Context, name string) ([]string, error)
	// DNSName is the name to resolve; default <node>.nodes.allstarlink.org.
	DNSName string
}

// Status is a snapshot for the UI.
type Status struct {
	Registered  bool      // last attempt was accepted
	LastAttempt time.Time // zero if none yet
	LastSuccess time.Time // zero if never
	// Since is when the node was first accepted at its current address
	// (it moves when the public IP changes); zero if never registered.
	Since      time.Time
	LastError  string // empty if the last attempt succeeded
	HTTPStatus int    // status of the last reply, 0 on transport error
	PublicIP   string // address the registrar saw
	DNSChecked bool   // a DNS lookup has been made for the current IP
	DNSOK      bool   // DNS lists PublicIP
	Failures   int    // consecutive failures
}

// Registrar runs the registration loop.
type Registrar struct {
	cfg Config
	cl  Clock
	hc  *http.Client

	mu     sync.Mutex
	st     Status
	cancel context.CancelFunc
	done   chan struct{}
	notify chan struct{}
}

// New validates cfg and returns a stopped Registrar.
func New(cfg Config) (*Registrar, error) {
	if strings.TrimSpace(cfg.Node) == "" || cfg.Password == "" {
		return nil, errors.New("register: node and password are required")
	}
	if cfg.URL == "" {
		cfg.URL = DefaultURL
	}
	if cfg.Interval <= 0 {
		cfg.Interval = DefaultInterval
	}
	if cfg.MaxBackoff <= 0 {
		cfg.MaxBackoff = DefaultMaxBack
	}
	if cfg.DNSName == "" {
		cfg.DNSName = cfg.Node + ".nodes.allstarlink.org"
	}
	r := &Registrar{cfg: cfg, cl: cfg.Clock, hc: cfg.HTTPClient, notify: make(chan struct{}, 1)}
	if r.cl == nil {
		r.cl = realClock{}
	}
	if r.hc == nil {
		r.hc = defaultClient()
	}
	return r, nil
}

func defaultClient() *http.Client {
	d := &net.Dialer{Timeout: DefaultTimeout}
	return &http.Client{
		Timeout: DefaultTimeout,
		Transport: &http.Transport{
			// IPv4 only until IPv6 is confirmed; DNS publishes only A records.
			DialContext: func(ctx context.Context, _, addr string) (net.Conn, error) {
				return d.DialContext(ctx, "tcp4", addr)
			},
			TLSHandshakeTimeout: DefaultTimeout,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// Start launches the loop; it registers at once. Calling Start twice is an error.
func (r *Registrar) Start(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		return errors.New("register: already started")
	}
	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	go r.loop(ctx, r.done)
	return nil
}

// Stop ends the loop and waits for it. No unregister is sent (the spec has
// none); the registration lapses on the server. Safe to call repeatedly.
func (r *Registrar) Stop() {
	r.mu.Lock()
	cancel, done := r.cancel, r.done
	r.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Status returns a snapshot.
func (r *Registrar) Status() Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.st
}

// Ready reports whether outgoing links may start: registered, and DNS (when
// a resolver is configured) shows the public IP.
func (r *Registrar) Ready() bool {
	s := r.Status()
	if !s.Registered {
		return false
	}
	return r.cfg.LookupHost == nil || s.DNSOK
}

// Changed returns a channel signalled (non-blocking, coalesced) after each
// attempt, for UI refresh.
func (r *Registrar) Changed() <-chan struct{} { return r.notify }

func (r *Registrar) loop(ctx context.Context, done chan struct{}) {
	defer close(done)
	for {
		r.attempt(ctx)
		select {
		case r.notify <- struct{}{}:
		default:
		}
		select {
		case <-ctx.Done():
			return
		case <-r.cl.After(r.nextDelay()):
		}
	}
}

// nextDelay is interval+1 s on success (per spec), else exponential backoff
// from the interval, capped at MaxBackoff (and never below the interval).
func (r *Registrar) nextDelay() time.Duration {
	r.mu.Lock()
	f := r.st.Failures
	r.mu.Unlock()
	if f == 0 {
		return r.cfg.Interval + time.Second
	}
	d := r.cfg.Interval
	for i := 1; i < f && d < r.cfg.MaxBackoff; i++ {
		d *= 2
	}
	if d > r.cfg.MaxBackoff {
		d = r.cfg.MaxBackoff
	}
	if d < r.cfg.Interval {
		d = r.cfg.Interval
	}
	return d
}

func (r *Registrar) attempt(ctx context.Context) {
	now := r.cl.Now()
	reply, code, err := r.post(ctx)
	r.mu.Lock()
	r.st.LastAttempt = now
	r.st.HTTPStatus = code
	if err != nil {
		r.st.Registered = false
		r.st.LastError = err.Error()
		r.st.Failures++
		r.mu.Unlock()
		return
	}
	oldIP := r.st.PublicIP
	r.st.Registered = true
	r.st.LastSuccess = now
	if r.st.Since.IsZero() || reply.IPAddr != oldIP {
		r.st.Since = now
	}
	r.st.LastError = ""
	r.st.Failures = 0
	r.st.PublicIP = reply.IPAddr
	if reply.IPAddr != oldIP {
		r.st.DNSChecked, r.st.DNSOK = false, false
	}
	needDNS := r.cfg.LookupHost != nil && !r.st.DNSOK
	ip := reply.IPAddr
	r.mu.Unlock()
	if needDNS {
		r.checkDNS(ctx, ip)
	}
}

func (r *Registrar) checkDNS(ctx context.Context, ip string) {
	addrs, err := r.cfg.LookupHost(ctx, r.cfg.DNSName)
	ok := false
	if err == nil {
		for _, a := range addrs {
			if a == ip {
				ok = true
			}
		}
	}
	r.mu.Lock()
	r.st.DNSChecked, r.st.DNSOK = true, ok
	r.mu.Unlock()
}

func (r *Registrar) post(ctx context.Context) (regReply, int, error) {
	var reply regReply
	body, err := buildBody(r.cfg.Node, r.cfg.Password, r.cfg.Port)
	if err != nil {
		return reply, 0, fmt.Errorf("register: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.cfg.URL, bytes.NewReader(body))
	if err != nil {
		return reply, 0, fmt.Errorf("register: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := r.hc.Do(req)
	if err != nil {
		// url.Error carries only the URL, never the body.
		return reply, 0, fmt.Errorf("register: post: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxReplyBytes))
	if err != nil {
		return reply, resp.StatusCode, fmt.Errorf("register: read reply: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return reply, resp.StatusCode, fmt.Errorf("register: registrar returned HTTP %d", resp.StatusCode)
	}
	if err := json.Unmarshal(raw, &reply); err != nil {
		return reply, resp.StatusCode, fmt.Errorf("register: decode reply: %w", err)
	}
	if !reply.ok() {
		return reply, resp.StatusCode, ErrNotRegistered
	}
	return reply, resp.StatusCode, nil
}
