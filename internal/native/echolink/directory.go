package echolink

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Directory is a logon to the EchoLink directory. It renews the logon in the
// background until Close and resolves stations for Connect.
type Directory struct {
	cfg Config

	poke chan struct{}
	stop chan struct{}
	done chan struct{}
	once sync.Once

	fetchMu sync.Mutex // one station list download at a time

	mu          sync.Mutex
	serverIdx   int
	server      string
	loggedIn    bool
	lastAttempt time.Time
	lastLogon   time.Time
	lastErr     string
	list        []Station
	listAt      time.Time
	connecting  bool
	active      *Conn
	closed      bool
}

// Status is a snapshot of the EchoLink client for the UI.
type Status struct {
	LoggedIn    bool        // the last logon was accepted
	Busy        bool        // we report BUSY because a connection is up
	Server      string      // directory server last used
	LastAttempt time.Time   // zero if none yet
	LastLogon   time.Time   // last accepted logon
	LastError   string      // empty if the last logon succeeded
	Stations    int         // entries in the cached station list
	ListUpdated time.Time   // when the station list was fetched
	Conn        *ConnStatus // nil when not connected
}

// Login logs on to the first directory server that accepts us and keeps the
// logon fresh until Close. It fails if no server accepts the logon.
func Login(ctx context.Context, cfg Config) (*Directory, error) {
	cfg, err := cfg.withDefaults()
	if err != nil {
		return nil, err
	}
	d := &Directory{
		cfg:  cfg,
		poke: make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	if err := d.logon(ctx, statusOnline); err != nil {
		return nil, err
	}
	go d.loop()
	return d, nil
}

// Status returns a snapshot for the UI.
func (d *Directory) Status() Status {
	d.mu.Lock()
	s := Status{
		LoggedIn:    d.loggedIn,
		Busy:        d.active != nil,
		Server:      d.server,
		LastAttempt: d.lastAttempt,
		LastLogon:   d.lastLogon,
		LastError:   d.lastErr,
		Stations:    len(d.list),
		ListUpdated: d.listAt,
	}
	active := d.active
	d.mu.Unlock()
	if active != nil {
		cs := active.Status()
		s.Conn = &cs
	}
	return s
}

// Stations returns the station list, downloading it if the cached copy is
// older than ListMaxAge.
func (d *Directory) Stations(ctx context.Context) ([]Station, error) {
	list, _, err := d.stations(ctx, false)
	return list, err
}

func (d *Directory) stations(ctx context.Context, force bool) (list []Station, fresh bool, err error) {
	d.fetchMu.Lock()
	defer d.fetchMu.Unlock()
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return nil, false, ErrClosed
	}
	if !force && d.list != nil && d.cfg.Clock.Now().Sub(d.listAt) < d.cfg.ListMaxAge {
		list = d.list
		d.mu.Unlock()
		return list, false, nil
	}
	d.mu.Unlock()

	err = d.request(ctx, func(c net.Conn) error {
		if _, err := c.Write([]byte("s")); err != nil {
			return fmt.Errorf("echolink: request station list: %w", err)
		}
		l, err := parseStationList(c)
		if err != nil {
			return err
		}
		list = l
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	d.mu.Lock()
	d.list = list
	d.listAt = d.cfg.Clock.Now()
	d.mu.Unlock()
	return list, true, nil
}

// Lookup resolves target, a callsign or EchoLink node number, to a station.
// A target missing from a cached list triggers one fresh download.
func (d *Directory) Lookup(ctx context.Context, target string) (Station, error) {
	list, fresh, err := d.stations(ctx, false)
	if err != nil {
		return Station{}, err
	}
	st, err := findStation(list, target)
	if err == nil || fresh || !errors.Is(err, ErrNotFound) {
		return st, err
	}
	if list, _, err = d.stations(ctx, true); err != nil {
		return Station{}, err
	}
	return findStation(list, target)
}

// Connect resolves target (a callsign, conference name or node number) and
// calls it. Only one connection may be up at a time; Close it before
// connecting elsewhere.
func (d *Directory) Connect(ctx context.Context, target string) (*Conn, error) {
	st, err := d.Lookup(ctx, target)
	if err != nil {
		return nil, err
	}
	if st.Callsign == d.cfg.Callsign {
		return nil, fmt.Errorf("echolink: cannot connect to ourselves (%s)", st.Callsign)
	}
	d.mu.Lock()
	switch {
	case d.closed:
		d.mu.Unlock()
		return nil, ErrClosed
	case d.active != nil || d.connecting:
		d.mu.Unlock()
		return nil, ErrBusy
	}
	d.connecting = true
	d.mu.Unlock()

	c, err := dial(ctx, d.cfg, st, d.connClosed)
	d.mu.Lock()
	d.connecting = false
	if err == nil {
		select {
		case <-c.finished:
			// Ended before we could record it; onClose already ran.
		default:
			d.active = c
		}
	}
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	d.wake()
	return c, nil
}

// connClosed is the Conn close hook.
func (d *Directory) connClosed(c *Conn) {
	d.mu.Lock()
	changed := d.active == c
	if changed {
		d.active = nil
	}
	d.mu.Unlock()
	if changed {
		d.wake()
	}
}

// wake asks the refresh loop to renew the logon now, to publish a status
// change.
func (d *Directory) wake() {
	select {
	case d.poke <- struct{}{}:
	default:
	}
}

// Close ends any connection, logs off and stops the refresh loop.
func (d *Directory) Close() error {
	var err error
	d.once.Do(func() {
		d.mu.Lock()
		d.closed = true
		active := d.active
		d.mu.Unlock()
		if active != nil {
			active.Close()
		}
		close(d.stop)
		<-d.done
		ctx, cancel := context.WithTimeout(context.Background(), d.cfg.RequestTimeout)
		defer cancel()
		err = d.logon(ctx, statusOffline)
	})
	return err
}

// loop renews the logon until Close.
func (d *Directory) loop() {
	defer close(d.done)
	for {
		d.mu.Lock()
		wait := d.cfg.Refresh
		if !d.loggedIn {
			wait = d.cfg.Retry
		}
		d.mu.Unlock()
		select {
		case <-d.stop:
			return
		case <-d.poke:
		case <-d.cfg.Clock.After(wait):
		}
		d.mu.Lock()
		status := statusOnline
		if d.active != nil {
			status = statusBusy
		}
		d.mu.Unlock()
		ctx, cancel := context.WithCancel(context.Background())
		go func() {
			select {
			case <-d.stop:
				cancel()
			case <-ctx.Done():
			}
		}()
		_ = d.logon(ctx, status)
		cancel()
	}
}

// logon sends one logon with the given status and records the outcome.
func (d *Directory) logon(ctx context.Context, status string) error {
	cmd := logonCommand(d.cfg.Callsign, d.cfg.Password, status, d.cfg.Location, d.cfg.Clock.Now())
	err := d.request(ctx, func(c net.Conn) error {
		if _, err := c.Write(cmd); err != nil {
			return fmt.Errorf("echolink: send logon: %w", err)
		}
		return readLogonReply(c)
	})
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastAttempt = d.cfg.Clock.Now()
	if status == statusOffline {
		d.loggedIn = false
		return err
	}
	if err != nil {
		d.loggedIn = false
		d.lastErr = err.Error()
		return err
	}
	d.loggedIn = true
	d.lastErr = ""
	d.lastLogon = d.lastAttempt
	return nil
}

// request runs fn on a fresh connection to each server in turn, starting
// with the last one that worked, until one succeeds. A refused logon is
// final: every server shares the same accounts.
func (d *Directory) request(ctx context.Context, fn func(net.Conn) error) error {
	d.mu.Lock()
	start := d.serverIdx
	d.mu.Unlock()
	var last error
	n := len(d.cfg.Servers)
	for i := 0; i < n; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("echolink: directory: %w", err)
		}
		idx := (start + i) % n
		server := d.cfg.Servers[idx]
		err := d.requestOne(ctx, server, fn)
		if err == nil {
			d.mu.Lock()
			d.serverIdx = idx
			d.server = server
			d.mu.Unlock()
			return nil
		}
		last = fmt.Errorf("%s: %w", server, err)
		if errors.Is(err, ErrBadLogon) {
			break
		}
	}
	return fmt.Errorf("echolink: directory: %w", last)
}

func (d *Directory) requestOne(ctx context.Context, server string, fn func(net.Conn) error) error {
	ctx, cancel := context.WithTimeout(ctx, d.cfg.RequestTimeout)
	defer cancel()
	c, err := d.cfg.Dial(ctx, "tcp4", server)
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	defer c.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	// Unblock reads if ctx is canceled before the deadline.
	stop := context.AfterFunc(ctx, func() { _ = c.SetDeadline(time.Unix(1, 0)) })
	defer stop()
	return fn(c)
}
