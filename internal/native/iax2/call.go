package iax2

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"net/netip"
	"sync"
	"time"
)

// Defaults for Config.
const (
	DefaultUsername     = "radio"
	DefaultDialTimeout  = 30 * time.Second
	DefaultPeerTimeout  = 60 * time.Second
	DefaultPingInterval = 20 * time.Second
	DefaultLagInterval  = 10 * time.Second
	DefaultRxLinger     = 500 * time.Millisecond
	DefaultLinkListTime = 30 * time.Second // app_rpt linkpost_time default
	// TxQueueMax caps queued transmit frames (1 s); the oldest is dropped.
	TxQueueMax = 50

	tickInterval     = FrameDuration
	initialRTO       = 500 * time.Millisecond
	minRTO           = 100 * time.Millisecond
	maxRTO           = 4 * time.Second
	maxRetries       = 5
	hangupLinger     = 2 * time.Second
	linkListShort    = 150 * time.Millisecond // app_rpt LINKLISTSHORTTIME
	redundantTxTime  = 2 * time.Second        // app_rpt REDUNDANT_TX_TIME
	redundantRxLimit = 5 * redundantTxTime
	eventQueue       = 64
	recvQueue        = 50
)

// Errors returned by Dial and Call.Err.
var (
	ErrRejected     = errors.New("iax2: call rejected")
	ErrHungUp       = errors.New("iax2: peer hung up")
	ErrTimeout      = errors.New("iax2: peer not responding")
	ErrAuth         = errors.New("iax2: authentication not possible")
	ErrFormat       = errors.New("iax2: peer chose an unsupported format")
	ErrClosed       = errors.New("iax2: call closed")
	ErrDisconnected = errors.New("iax2: peer asked to disconnect")
)

// Config describes one outgoing link.
type Config struct {
	LocalNode  string         // our node number, sent as the calling number
	RemoteNode string         // the node we link to, sent as the called number
	Addr       netip.AddrPort // the far node's IAX2 address (see Resolver)
	// Secret answers an AUTHREQ. Stock app_rpt [radio] users do not ask
	// for one; it is used only if the peer demands MD5 or plaintext auth.
	Secret string
	// Callsign is sent as the calling name. Optional.
	Callsign string
	// Username is sent in USERNAME; default "radio", as app_rpt dials.
	Username string
	// ListenAddr is the local bind address. The zero value binds an
	// ephemeral port on all addresses of Addr's family.
	ListenAddr netip.AddrPort

	DialTimeout  time.Duration // until ANSWER; default DefaultDialTimeout
	PeerTimeout  time.Duration // silence before the call is dropped
	PingInterval time.Duration
	LagInterval  time.Duration
	RxLinger     time.Duration // voice gap that ends a far-end keyup
	LinkListTime time.Duration // period of our "L" message
	Jitter       JitterConfig

	Clock Clock // default the real clock
}

// EventKind classifies a call Event.
type EventKind int

// Event kinds.
const (
	// EventRxKey: the far side keyed or unkeyed (Event.Keyed).
	EventRxKey EventKind = iota + 1
	// EventLinkList: the far side sent a new link list (Event.Links).
	EventLinkList
	// EventText: other link text (Event.Text), such as messages.
	EventText
)

// Event is something the far side did.
type Event struct {
	Kind  EventKind
	Keyed bool
	Links []LinkEntry
	Text  LinkText
}

// Stats are call counters.
type Stats struct {
	TxVoice     uint64 // voice frames sent
	RxVoice     uint64 // voice frames received
	TxDropped   uint64 // transmit queue overflow
	RxDropped   uint64 // receive channel full
	Retransmits uint64
	Foreign     uint64 // datagrams from other sources, other calls or malformed
	Rejected    uint64 // incoming NEW calls refused
	RTT         time.Duration
	Lag         time.Duration // last LAGRQ round trip
	Jitter      JitterStats
	LastRx      time.Time
}

// Call is one outgoing IAX2 link. Its methods are safe for concurrent use.
type Call struct {
	cfg       Config
	clock     Clock
	conn      *net.UDPConn
	peer      netip.AddrPort
	localCall uint16

	inbound  chan inbound
	cmds     chan command
	recv     chan Frame
	events   chan Event
	answered chan struct{}
	done     chan struct{}

	errOnce sync.Once
	err     error

	mu     sync.Mutex
	txq    []Frame
	links  []LinkEntry
	rxKey  bool
	stats  Stats
	closed bool

	s session // owned by the loop goroutine
}

type inbound struct {
	full *FullFrame
	mini *MiniFrame
	at   time.Time
}

type cmdKind int

const (
	cmdKey cmdKind = iota + 1
	cmdText
	cmdLinkList
	cmdHangup
	cmdAbort
)

type command struct {
	kind  cmdKind
	on    bool
	text  string
	links []LinkEntry
	reply chan error
}

// Dial places a call and returns once the far node answers. The context
// bounds the dial only; use Hangup to end the call.
func Dial(ctx context.Context, cfg Config) (*Call, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	cfg.setDefaults()
	peer := netip.AddrPortFrom(cfg.Addr.Addr().Unmap(), cfg.Addr.Port())
	network, laddr := "udp4", cfg.ListenAddr
	if peer.Addr().Is6() {
		network = "udp6"
	}
	var ua *net.UDPAddr
	if laddr.IsValid() {
		ua = net.UDPAddrFromAddrPort(laddr)
	}
	conn, err := net.ListenUDP(network, ua)
	if err != nil {
		return nil, fmt.Errorf("iax2: bind: %w", err)
	}
	c := &Call{
		cfg:       cfg,
		clock:     cfg.Clock,
		conn:      conn,
		peer:      peer,
		localCall: uint16(1 + rand.IntN(callNoMask)),
		inbound:   make(chan inbound, 256),
		cmds:      make(chan command, 16),
		recv:      make(chan Frame, recvQueue),
		events:    make(chan Event, eventQueue),
		answered:  make(chan struct{}),
		done:      make(chan struct{}),
	}
	c.s.init(c)
	ticker := c.clock.NewTicker(tickInterval)
	go c.readLoop()
	go c.loop(ticker)
	select {
	case <-c.answered:
		return c, nil
	case <-c.done:
		return nil, c.Err()
	case <-ctx.Done():
		c.do(command{kind: cmdAbort})
		return nil, fmt.Errorf("iax2: dial %s: %w", cfg.RemoteNode, ctx.Err())
	}
}

func (cfg *Config) validate() error {
	if !allDigits(cfg.LocalNode) {
		return fmt.Errorf("iax2: bad local node %q", cfg.LocalNode)
	}
	if !allDigits(cfg.RemoteNode) {
		return fmt.Errorf("iax2: bad remote node %q", cfg.RemoteNode)
	}
	if !cfg.Addr.IsValid() || cfg.Addr.Port() == 0 {
		return fmt.Errorf("iax2: bad peer address %v", cfg.Addr)
	}
	return nil
}

func (cfg *Config) setDefaults() {
	if cfg.Username == "" {
		cfg.Username = DefaultUsername
	}
	def := func(d *time.Duration, v time.Duration) {
		if *d <= 0 {
			*d = v
		}
	}
	def(&cfg.DialTimeout, DefaultDialTimeout)
	def(&cfg.PeerTimeout, DefaultPeerTimeout)
	def(&cfg.PingInterval, DefaultPingInterval)
	def(&cfg.LagInterval, DefaultLagInterval)
	def(&cfg.RxLinger, DefaultRxLinger)
	def(&cfg.LinkListTime, DefaultLinkListTime)
	if cfg.Clock == nil {
		cfg.Clock = realClock{}
	}
}

// do hands a command to the loop and waits for its reply.
func (c *Call) do(cmd command) error {
	cmd.reply = make(chan error, 1)
	select {
	case c.cmds <- cmd:
	case <-c.done:
		return ErrClosed
	}
	select {
	case err := <-cmd.reply:
		return err
	case <-c.done:
		if cmd.kind == cmdHangup || cmd.kind == cmdAbort {
			return nil
		}
		return ErrClosed
	}
}

// Send queues one frame for transmission. Frames are sent only while the
// call is keyed (Key(true)); while keyed and the queue is empty, silence is
// sent so the far node stays keyed, as app_rpt does.
func (c *Call) Send(f Frame) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	if len(c.txq) >= TxQueueMax {
		c.txq = c.txq[1:]
		c.stats.TxDropped++
	}
	c.txq = append(c.txq, f)
}

// Recv returns received voice, played out of the jitter buffer every 20 ms
// while the far side sends. It is closed when the call ends.
func (c *Call) Recv() <-chan Frame { return c.recv }

// Events returns far-end events. It is closed when the call ends. If the
// reader falls behind the oldest events are dropped.
func (c *Call) Events() <-chan Event { return c.events }

// Key starts (true) or stops (false) transmitting to the far node.
func (c *Call) Key(on bool) error { return c.do(command{kind: cmdKey, on: on}) }

// SetLinkList sets the nodes we report behind us in our "L" message, such
// as an EchoLink connection. Our own link to the far node is never listed.
func (c *Call) SetLinkList(entries []LinkEntry) error {
	if _, err := FormatLinkList(entries); err != nil {
		return err
	}
	return c.do(command{kind: cmdLinkList, links: append([]LinkEntry(nil), entries...)})
}

// SendText sends a raw link text message.
func (c *Call) SendText(s string) error { return c.do(command{kind: cmdText, text: s}) }

// RxKeyed reports whether the far side is keyed.
func (c *Call) RxKeyed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rxKey
}

// Links returns the far node's last link list.
func (c *Call) Links() []LinkEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]LinkEntry(nil), c.links...)
}

// Stats returns a snapshot of the counters.
func (c *Call) Stats() Stats {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.stats
}

// LocalAddr returns the bound UDP address.
func (c *Call) LocalAddr() netip.AddrPort {
	return c.conn.LocalAddr().(*net.UDPAddr).AddrPort()
}

// Hangup ends the call, waiting briefly for the peer to acknowledge.
func (c *Call) Hangup() error { return c.do(command{kind: cmdHangup}) }

// Done is closed when the call has ended.
func (c *Call) Done() <-chan struct{} { return c.done }

// Err returns why the call ended: nil after a local Hangup, otherwise an
// error wrapping one of the Err* values. It is nil while the call is up.
func (c *Call) Err() error {
	select {
	case <-c.done:
		return c.err
	default:
		return nil
	}
}

// readLoop decodes datagrams from the peer and refuses incoming calls.
func (c *Call) readLoop() {
	for {
		buf := make([]byte, 1500)
		n, from, err := c.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			select {
			case <-c.done:
				return
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		from = netip.AddrPortFrom(from.Addr().Unmap(), from.Port())
		full, mini, err := Decode(buf[:n])
		if err != nil {
			c.count(func(s *Stats) { s.Foreign++ })
			continue
		}
		if full != nil && full.Type == TypeIAX && full.Subclass == CmdNew {
			c.refuse(full, from)
			continue
		}
		if from != c.peer {
			c.count(func(s *Stats) { s.Foreign++ })
			continue
		}
		select {
		case c.inbound <- inbound{full: full, mini: mini, at: c.clock.Now()}:
		case <-c.done:
			return
		}
	}
}

// refuse answers an incoming NEW with REJECT. The node is outbound only.
func (c *Call) refuse(f *FullFrame, from netip.AddrPort) {
	c.count(func(s *Stats) { s.Rejected++ })
	var ies IEs
	ies.AddString(IECause, "Outbound-only node")
	ies.AddU8(IECauseCode, 21) // call rejected
	data, _ := ies.Encode()
	r := FullFrame{
		SrcCall: 0, DstCall: f.SrcCall, TS: f.TS,
		OSeq: f.ISeq, ISeq: f.OSeq + 1,
		Type: TypeIAX, Subclass: CmdReject, Data: data,
	}
	b, err := r.Append(nil)
	if err == nil {
		_, _ = c.conn.WriteToUDPAddrPort(b, from)
	}
}

func (c *Call) count(f func(*Stats)) {
	c.mu.Lock()
	f(&c.stats)
	c.mu.Unlock()
}

func (c *Call) emit(ev Event) {
	for {
		select {
		case c.events <- ev:
			return
		default:
		}
		select {
		case <-c.events:
		default:
		}
	}
}
