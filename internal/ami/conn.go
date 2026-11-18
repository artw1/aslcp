package ami

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config configures a connection (and, for Client, the reconnect policy).
type Config struct {
	Addr     string // host:port, e.g. "127.0.0.1:5038"
	Username string
	Secret   string
	// Events is the Login "Events" header, e.g. "call", "system,call" or
	// "off". Empty means "off".
	Events string
	// DialTimeout bounds TCP connect, banner and login. Default 10s.
	DialTimeout time.Duration
	// PingInterval is the keepalive period; 0 disables keepalive. A ping is
	// skipped when other traffic arrived within the last interval.
	PingInterval time.Duration
	// ActionTimeout is applied to Action/Command calls whose context has no
	// deadline. Default 30s.
	ActionTimeout time.Duration
	// ReconnectMin and ReconnectMax bound Client's exponential backoff.
	// Defaults 1s and 30s.
	ReconnectMin time.Duration
	ReconnectMax time.Duration
}

func (c Config) withDefaults() Config {
	if c.DialTimeout <= 0 {
		c.DialTimeout = 10 * time.Second
	}
	if c.ActionTimeout <= 0 {
		c.ActionTimeout = 30 * time.Second
	}
	if c.ReconnectMin <= 0 {
		c.ReconnectMin = time.Second
	}
	if c.ReconnectMax <= 0 {
		c.ReconnectMax = 30 * time.Second
	}
	if c.ReconnectMax < c.ReconnectMin {
		c.ReconnectMax = c.ReconnectMin
	}
	if c.Events == "" {
		c.Events = "off"
	}
	return c
}

var (
	// ErrAuth is returned (wrapped) when the Login action is rejected.
	ErrAuth = errors.New("ami: authentication failed")
	// ErrBanner is returned when the peer is not an Asterisk Call Manager.
	ErrBanner = errors.New("ami: peer is not an Asterisk Call Manager")
	// ErrClosed is returned for use of a closed connection.
	ErrClosed = errors.New("ami: connection closed")
	// ErrNotConnected is returned by Client when no connection is up.
	ErrNotConnected = errors.New("ami: not connected")
	// ErrLostAfterSend is wrapped into the error returned when the connection
	// closed after an action was written but before its reply arrived, so the
	// server may or may not have processed it. Errors before the write never
	// wrap it.
	ErrLostAfterSend = errors.New("ami: connection lost after action was sent")
)

// ResponseError is returned for a "Response: Error" reply. Msg is the raw
// reply.
type ResponseError struct {
	Action string
	Text   string // server message, secret scrubbed
	Msg    *Message
}

func (e *ResponseError) Error() string {
	return "ami: action " + e.Action + " failed: " + e.Text
}

const bannerPrefix = "Asterisk Call Manager/"

// abandonGrace is how long a timed-out action may keep the connection busy
// waiting for its late reply before the connection is declared dead.
const abandonGrace = 30 * time.Second

type pending struct {
	id        string
	ch        chan *Message
	abandoned bool        // caller gave up (guarded by Conn.mu)
	timer     *time.Timer // abandonment timer (guarded by Conn.mu)
	once      sync.Once
	release   func()
}

func (p *pending) finish() { p.once.Do(p.release) }

// Conn is one authenticated AMI connection.
//
// Only one action is in flight at a time (calls serialize on an internal
// semaphore); events flow independently. That makes it safe to correlate
// replies that lack an ActionID (such as app_rpt's RptLocalNodes XML): a reply
// without ActionID is attributed to the single outstanding action, while a
// reply with a non-matching ActionID (a late answer to an action whose caller
// timed out) is discarded.
type Conn struct {
	cfg     Config
	nc      net.Conn
	version string
	hook    func(*Message)

	events fan[*Message]

	wmu sync.Mutex
	sem chan struct{}

	mu      sync.Mutex
	pending *pending

	done      chan struct{}
	loopDone  chan struct{}
	closeOnce sync.Once
	err       error

	lastRead  atomic.Int64
	seq       atomic.Uint64
	idPrefix  string
	stray     atomic.Uint64
	pingEvery time.Duration
}

// Dial connects, validates the banner and logs in.
func Dial(ctx context.Context, cfg Config) (*Conn, error) { return dial(ctx, cfg, nil) }

func dial(ctx context.Context, cfg Config, hook func(*Message)) (*Conn, error) {
	cfg = cfg.withDefaults()
	d := net.Dialer{Timeout: cfg.DialTimeout}
	nc, err := d.DialContext(ctx, "tcp", cfg.Addr)
	if err != nil {
		return nil, fmt.Errorf("ami: dial %s: %w", cfg.Addr, err)
	}
	stop := context.AfterFunc(ctx, func() { nc.Close() })
	defer stop()

	r := bufio.NewReaderSize(nc, 64*1024)
	_ = nc.SetReadDeadline(time.Now().Add(cfg.DialTimeout))
	line, err := readLine(r)
	if err != nil {
		nc.Close()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("ami: reading banner: %w", err)
	}
	if !strings.HasPrefix(line, bannerPrefix) {
		nc.Close()
		return nil, ErrBanner
	}
	_ = nc.SetReadDeadline(time.Time{})

	var pfx [6]byte
	_, _ = rand.Read(pfx[:])
	c := &Conn{
		cfg:       cfg,
		nc:        nc,
		version:   strings.TrimSpace(strings.TrimPrefix(line, bannerPrefix)),
		hook:      hook,
		sem:       make(chan struct{}, 1),
		done:      make(chan struct{}),
		loopDone:  make(chan struct{}),
		idPrefix:  hex.EncodeToString(pfx[:]),
		pingEvery: cfg.PingInterval,
	}
	c.lastRead.Store(time.Now().UnixNano())
	go c.readLoop(r)

	lctx, cancel := context.WithTimeout(ctx, cfg.DialTimeout)
	defer cancel()
	_, err = c.Action(lctx, "Login",
		H("Username", cfg.Username), H("Secret", cfg.Secret), H("Events", cfg.Events))
	if err != nil {
		c.closeWith(ErrClosed)
		<-c.loopDone
		var re *ResponseError
		if errors.As(err, &re) {
			return nil, fmt.Errorf("%w: %s", ErrAuth, re.Text)
		}
		return nil, c.scrubErr(err)
	}
	if c.pingEvery > 0 {
		go c.pingLoop()
	}
	return c, nil
}

// Version returns the banner version (the text after "Asterisk Call Manager/").
func (c *Conn) Version() string { return c.version }

// Done is closed when the connection is closed for any reason.
func (c *Conn) Done() <-chan struct{} { return c.done }

// Err returns why the connection closed (nil while open).
func (c *Conn) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// Subscribe returns a channel of event messages (those with an Event header)
// with the given buffer size, and a cancel function. The channel is closed on
// cancel or when the connection closes. The reader never blocks: when the
// buffer is full the event is dropped and counted (see Dropped).
func (c *Conn) Subscribe(buf int) (<-chan *Message, func()) {
	return c.events.subscribe(buf, nil)
}

// Dropped returns the number of events dropped because a subscriber was slow.
func (c *Conn) Dropped() uint64 { return c.events.dropped.Load() }

// Stray returns the number of replies that matched no outstanding action.
func (c *Conn) Stray() uint64 { return c.stray.Load() }

func (c *Conn) scrub(s string) string {
	if c.cfg.Secret != "" {
		s = strings.ReplaceAll(s, c.cfg.Secret, "***")
	}
	return s
}

func (c *Conn) scrubErr(err error) error {
	if err == nil || c.cfg.Secret == "" || !strings.Contains(err.Error(), c.cfg.Secret) {
		return err
	}
	return errors.New(c.scrub(err.Error()))
}

func (c *Conn) closeWith(err error) {
	c.closeOnce.Do(func() {
		c.err = c.scrubErr(err)
		close(c.done)
		c.nc.Close()
		c.events.closeAll()
	})
}

// Close logs off (best effort) and closes the connection.
func (c *Conn) Close() error {
	select {
	case <-c.done:
	default:
		c.wmu.Lock()
		_ = c.nc.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = c.nc.Write([]byte("Action: Logoff\r\n\r\n"))
		c.wmu.Unlock()
	}
	c.closeWith(ErrClosed)
	<-c.loopDone
	return nil
}

func (c *Conn) nextID() string {
	return c.idPrefix + "-" + strconv.FormatUint(c.seq.Add(1), 10)
}

func (c *Conn) readLoop(r *bufio.Reader) {
	defer close(c.loopDone)
	for {
		m, err := readMessage(r)
		if err != nil {
			c.closeWith(fmt.Errorf("ami: read: %w", err))
			return
		}
		c.lastRead.Store(time.Now().UnixNano())
		if m.Event() != "" {
			c.events.publish(m)
			if c.hook != nil {
				c.hook(m)
			}
			continue
		}
		c.deliver(m)
	}
}

func (c *Conn) deliver(m *Message) {
	c.mu.Lock()
	defer c.mu.Unlock()
	p := c.pending
	if p == nil {
		c.stray.Add(1)
		return
	}
	if id := m.Get("ActionID"); id != "" && id != p.id {
		c.stray.Add(1)
		return
	}
	c.pending = nil
	p.ch <- m
	if p.abandoned {
		if p.timer != nil {
			p.timer.Stop()
		}
		p.finish()
	}
}

func validToken(s string) bool { return !strings.ContainsAny(s, "\r\n") }

func (c *Conn) write(name, id string, headers []Header) error {
	var sb strings.Builder
	sb.WriteString("Action: " + name + "\r\nActionID: " + id + "\r\n")
	for _, h := range headers {
		sb.WriteString(h.Key + ": " + h.Value + "\r\n")
	}
	sb.WriteString("\r\n")
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_ = c.nc.SetWriteDeadline(time.Now().Add(10 * time.Second))
	_, err := c.nc.Write([]byte(sb.String()))
	return err
}

func (c *Conn) acquire(ctx context.Context) error {
	select {
	case c.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-c.done:
		return c.closedErr()
	}
}

func (c *Conn) closedErr() error {
	if c.err != nil {
		return c.err
	}
	return ErrClosed
}

func (c *Conn) withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	if _, ok := ctx.Deadline(); ok {
		return ctx, func() {}
	}
	return context.WithTimeout(ctx, c.cfg.ActionTimeout)
}

// roundTrip sends one action and waits for its reply. The semaphore must be
// held; roundTrip arranges for it to be released.
func (c *Conn) roundTrip(ctx context.Context, name string, headers []Header) (*Message, error) {
	p := &pending{id: c.nextID(), ch: make(chan *Message, 1), release: func() { <-c.sem }}
	select {
	case <-c.done:
		p.finish()
		return nil, c.closedErr()
	default:
	}
	c.mu.Lock()
	c.pending = p
	c.mu.Unlock()

	if err := c.write(name, p.id, headers); err != nil {
		c.mu.Lock()
		c.pending = nil
		c.mu.Unlock()
		p.finish()
		c.closeWith(fmt.Errorf("ami: write: %w", err))
		return nil, c.closedErr()
	}
	select {
	case m := <-p.ch:
		p.finish()
		return m, nil
	case <-c.done:
		// The server may send a reply and close right away (e.g. after a
		// rejected login). Both channels are then ready and select picks
		// at random, so prefer a reply that has already arrived.
		select {
		case m := <-p.ch:
			p.finish()
			return m, nil
		default:
		}
		p.finish()
		return nil, fmt.Errorf("%w: %w", ErrLostAfterSend, c.closedErr())
	case <-ctx.Done():
		c.mu.Lock()
		if c.pending == p {
			// Still waiting: keep the semaphore until the late reply
			// arrives so it cannot be mistaken for the next action's.
			p.abandoned = true
			p.timer = time.AfterFunc(abandonGrace, func() {
				c.mu.Lock()
				stuck := c.pending == p
				c.mu.Unlock()
				if stuck {
					c.closeWith(errors.New("ami: no reply to timed-out action"))
				}
			})
			c.mu.Unlock()
			return nil, ctx.Err()
		}
		c.mu.Unlock()
		m := <-p.ch // delivered concurrently with the timeout
		p.finish()
		return m, nil
	}
}

// Action sends an action and waits for its reply. A "Response: Error" reply
// yields both the message and a *ResponseError. Replies without a Response
// header (for example RptLocalNodes) are returned as-is.
func (c *Conn) Action(ctx context.Context, name string, headers ...Header) (*Message, error) {
	if name == "" || !validToken(name) {
		return nil, errors.New("ami: invalid action name")
	}
	for _, h := range headers {
		if h.Key == "" || strings.ContainsAny(h.Key, ":\r\n") || !validToken(h.Value) {
			return nil, errors.New("ami: invalid header (empty key or CR/LF/colon in key or CR/LF in value)")
		}
	}
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return nil, c.scrubErr(err)
	}
	m, err := c.roundTrip(ctx, name, headers)
	if err != nil {
		return nil, c.scrubErr(err)
	}
	if strings.EqualFold(m.Response(), "Error") {
		text := m.Get("Message")
		if text == "" {
			text = strings.Join(m.All("Output"), "; ")
		}
		return m, &ResponseError{Action: name, Text: c.scrub(text), Msg: m}
	}
	return m, nil
}

// Command runs a CLI command and returns its output lines. Both the modern
// format (Response: Success with Output: lines) and the legacy format
// (Response: Follows, text until --END COMMAND--) are supported.
func (c *Conn) Command(ctx context.Context, cmd string) ([]string, error) {
	m, err := c.Action(ctx, "Command", H("Command", cmd))
	if err != nil {
		return nil, err
	}
	var lines []string
	if strings.EqualFold(m.Response(), "Follows") {
		lines = append(lines, m.Body...)
	} else {
		lines = append(lines, m.All("Output")...)
		lines = append(lines, m.Body...)
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

func (c *Conn) pingLoop() {
	t := time.NewTicker(c.pingEvery)
	defer t.Stop()
	timeout := c.pingEvery
	if timeout < 2*time.Second {
		timeout = 2 * time.Second
	}
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		if time.Since(time.Unix(0, c.lastRead.Load())) < c.pingEvery {
			continue // recent traffic proves liveness
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		if err := c.acquire(ctx); err != nil {
			cancel()
			continue // busy with another action; try next tick
		}
		_, err := c.roundTrip(ctx, "Ping", nil)
		cancel()
		if err != nil {
			c.closeWith(fmt.Errorf("ami: keepalive failed: %w", err))
			return
		}
	}
}
