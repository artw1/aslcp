package ami

import (
	"context"
	"math/rand/v2"
	"sync"
	"time"
)

// State describes the connection state of a Client.
type State struct {
	Connected bool
	// Err is the last connection error (nil while connected or before the
	// first failure).
	Err error
	// Since is when this state began.
	Since time.Time
	// Attempts counts connection attempts so far.
	Attempts int
	// Version is the AMI banner version while connected.
	Version string
}

// Client maintains a connection with automatic reconnect and exponential
// backoff (Config.ReconnectMin..ReconnectMax, with jitter). Event
// subscriptions survive reconnects. Close logs off.
type Client struct {
	cfg    Config
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu       sync.Mutex
	conn     *Conn
	state    State
	closed   bool
	connWait chan struct{} // closed and replaced on every state change

	events fan[*Message]
	states fan[State]
}

// NewClient starts connecting in the background and returns immediately.
func NewClient(cfg Config) *Client {
	cfg = cfg.withDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{cfg: cfg, cancel: cancel, connWait: make(chan struct{})}
	c.states.keepLatest = true
	c.state = State{Since: time.Now()}
	c.wg.Add(1)
	go c.run(ctx)
	return c
}

func (c *Client) setState(s State, conn *Conn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state = s
	c.conn = conn
	close(c.connWait)
	c.connWait = make(chan struct{})
	c.states.publish(s)
}

func jitter(d time.Duration) time.Duration {
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

func (c *Client) run(ctx context.Context) {
	defer c.wg.Done()
	backoff := c.cfg.ReconnectMin
	attempts := 0
	sleep := func(d time.Duration) bool {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return false
		case <-t.C:
			return true
		}
	}
	for ctx.Err() == nil {
		attempts++
		conn, err := dial(ctx, c.config(), c.events.publish)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.setState(State{Err: err, Since: time.Now(), Attempts: attempts}, nil)
			if !sleep(jitter(backoff)) {
				return
			}
			if backoff *= 2; backoff > c.cfg.ReconnectMax {
				backoff = c.cfg.ReconnectMax
			}
			continue
		}
		backoff = c.cfg.ReconnectMin
		c.setState(State{Connected: true, Since: time.Now(), Attempts: attempts, Version: conn.Version()}, conn)
		select {
		case <-conn.Done():
			c.setState(State{Err: conn.Err(), Since: time.Now(), Attempts: attempts}, nil)
			if !sleep(jitter(backoff)) {
				return
			}
		case <-ctx.Done():
			conn.Close()
			return
		}
	}
}

func (c *Client) config() Config {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cfg
}

// SetLogin replaces the credentials used by the next connection attempt. The
// current connection is not touched; call Reconnect to use them now.
func (c *Client) SetLogin(username, secret string) {
	c.mu.Lock()
	c.cfg.Username, c.cfg.Secret = username, secret
	c.mu.Unlock()
}

// Reconnect drops the current connection (if any); the client logs in again
// after the usual short wait, with the credentials of SetLogin.
func (c *Client) Reconnect() {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

// State returns the current connection state.
func (c *Client) State() State {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.state
}

// SubscribeState returns a channel of state changes. The current state is
// delivered first. If the subscriber is slow the oldest queued state is
// discarded, so the latest state is never lost.
func (c *Client) SubscribeState(buf int) (<-chan State, func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.state
	return c.states.subscribe(buf, &s)
}

// Subscribe returns a channel of event messages that continues across
// reconnects. It is closed on cancel or Client.Close.
func (c *Client) Subscribe(buf int) (<-chan *Message, func()) {
	return c.events.subscribe(buf, nil)
}

// Dropped returns events dropped for slow subscribers (all connections).
func (c *Client) Dropped() uint64 { return c.events.dropped.Load() }

func (c *Client) current() (*Conn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, ErrClosed
	}
	if c.conn == nil {
		return nil, ErrNotConnected
	}
	return c.conn, nil
}

// Action performs an action on the current connection (ErrNotConnected if
// there is none). It is not retried across reconnects.
func (c *Client) Action(ctx context.Context, name string, headers ...Header) (*Message, error) {
	conn, err := c.current()
	if err != nil {
		return nil, err
	}
	return conn.Action(ctx, name, headers...)
}

// Command runs a CLI command on the current connection.
func (c *Client) Command(ctx context.Context, cmd string) ([]string, error) {
	conn, err := c.current()
	if err != nil {
		return nil, err
	}
	return conn.Command(ctx, cmd)
}

// WaitConnected blocks until connected, ctx is done, or the client is closed.
func (c *Client) WaitConnected(ctx context.Context) error {
	for {
		c.mu.Lock()
		closed, ok, ch := c.closed, c.state.Connected, c.connWait
		c.mu.Unlock()
		if closed {
			return ErrClosed
		}
		if ok {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close stops reconnecting, logs off and closes subscriber channels.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	c.mu.Unlock()
	c.cancel()
	c.wg.Wait()
	c.setState(State{Err: ErrClosed, Since: time.Now(), Attempts: c.state.Attempts}, nil)
	c.events.closeAll()
	c.states.closeAll()
	return nil
}
