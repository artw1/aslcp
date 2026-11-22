// Package bm is the BrandMeister network (ctl.ModeBM). It speaks BrandMeister's
// Rewind protocol ("REWIND01", the Simple External Application interface,
// UDP, default port 54005), not the Homebrew/MMDVM protocol. The wire format
// is documented in rewind.go; the implementation is original (go-brandmeister
// and DigestPlay, both MIT, were used as protocol references only).
//
// Rewind carries no DMR bursts: a call is a super header (source and
// destination ID plus callsigns), optionally a 12 byte voice header LC, audio
// packets of three AMBE frames, and a terminator. This package asks the
// server for linear frames (option bit 1: 49-bit AMBE without FEC), which are
// exactly the 7-byte md380 frames the bridge uses, so no FEC code is needed
// and the internal/dv/dmr package is not used. A server that sends FEC coded
// "mode 33" frames (27 byte audio packets) is not supported: those packets
// are counted and dropped.
//
// Login: keep-alive (carries client id and software description) -> server
// challenge (salt) -> authentication = SHA-256(salt || password) -> empty
// keep-alive -> configuration (options) -> configuration reply = logged in.
// Failed or lost sessions are retried with exponential backoff 1 s .. 60 s.
//
// Network options (ctl.NetConfig.Options), all optional:
//
//	client_id        Rewind application id sent in every keep-alive. BrandMeister
//	                 allocates these to software authors: an unregistered id is
//	                 likely to be refused. Default: repeater_id.
//	client_version   software description text (default "aslcp-dv").
//	repeater_id      default for client_id; default Identity.RepeaterID, else
//	                 Identity.DMRID*100+1.
//	slot             1 or 2 (default 2). Rewind has no timeslots; reported in
//	                 call headers only.
//	colour_code      0..15 (default 1). Not carried by Rewind; accepted for
//	                 config compatibility.
//	super_header     "false" disables the server's super header (callsigns).
//	keepalive_ms     keep-alive period (default 5000).
//	timeout_ms       session considered lost after this silence (default 15000).
//	login_timeout_ms one login attempt (default 5000).
//	backoff_min_ms   first retry delay (default 1000).
//	backoff_max_ms   retry delay cap (default 60000).
//	rx_timeout_ms    end a received call without terminator after this
//	                 (default 1500).
//
// NetConfig.Address is required (there is no default host; use the master
// host name BrandMeister gave you); Port defaults to 54005. The password is
// never logged and never reported in Status.
//
// Tune: a group target subscribes to the talkgroup (session type 7). TG 9990
// (the parrot) and Private targets are private calls: the node subscribes to
// private voice for its own DMR ID (type 5) so replies reach it, and TX goes
// to the target ID as a private call. Unlink cancels the subscription (Rewind
// has no TG 4000 "disconnect" concept; cancelling is the unlink).
package bm

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/network"
)

// ParrotTG is BrandMeister's echo test target (a private call).
const ParrotTG = 9990

// Defaults.
const (
	DefaultKeepAlive    = 5 * time.Second
	DefaultTimeout      = 15 * time.Second
	DefaultLoginTimeout = 5 * time.Second
	DefaultBackoffMin   = time.Second
	DefaultBackoffMax   = 60 * time.Second
	DefaultRXTimeout    = 1500 * time.Millisecond
)

var (
	errBadPassword = errors.New("authentication refused")
	errLost        = errors.New("connection lost")
	errLoginTO     = errors.New("login timeout")
)

func init() {
	network.Register(ctl.ModeBM, func(cfg ctl.NetConfig, id ctl.Identity) (network.Network, error) {
		return New(cfg, id)
	})
}

// Net is the BrandMeister network.
type Net struct {
	enabled  bool
	addr     string
	password string
	id       ctl.Identity
	clientID uint32
	version  string
	slot     int
	options  uint32

	keepAlive, timeout, loginTO, bMin, bMax, rxTO time.Duration

	mu       sync.Mutex
	conn     *net.UDPConn
	seq      [2]uint32
	running  bool
	login    ctl.LoginState
	detail   string
	logged   bool
	sink     network.Sink
	sub      *subscription // desired subscription
	nextCall uint64

	txCall   uint64
	txActive bool
	txBuf    []byte
	txDst    uint32

	dropped uint64 // mode 33 packets
}

type subscription struct {
	typ, target uint32
	tg          uint32
	private     bool
}

// New builds the network.
func New(cfg ctl.NetConfig, id ctl.Identity) (*Net, error) {
	if cfg.Address == "" {
		return nil, errors.New("bm: address (master host) is required")
	}
	port := cfg.Port
	if port == 0 {
		port = defaultPort
	}
	if port < 0 || port > 65535 {
		return nil, errors.New("bm: bad port")
	}
	o := cfg.Options
	n := &Net{
		enabled: cfg.Enabled, password: cfg.Password, id: id,
		addr:    net.JoinHostPort(cfg.Address, strconv.Itoa(port)),
		version: "aslcp-dv", slot: 2, options: optSuperHeader | optLinearFrame,
		keepAlive: DefaultKeepAlive, timeout: DefaultTimeout, loginTO: DefaultLoginTimeout,
		bMin: DefaultBackoffMin, bMax: DefaultBackoffMax, rxTO: DefaultRXTimeout,
		login: ctl.LoginOff, nextCall: 1 << 33,
	}
	rep := id.RepeaterID
	if v, ok := o["repeater_id"]; ok {
		u, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("bm: repeater_id: %v", err)
		}
		rep = uint32(u)
	}
	if rep == 0 {
		rep = id.DMRID*100 + 1
	}
	n.clientID = rep
	if v, ok := o["client_id"]; ok {
		u, err := strconv.ParseUint(v, 10, 32)
		if err != nil {
			return nil, fmt.Errorf("bm: client_id: %v", err)
		}
		n.clientID = uint32(u)
	}
	if v := o["client_version"]; v != "" {
		n.version = v
	}
	if v, ok := o["slot"]; ok {
		s, err := strconv.Atoi(v)
		if err != nil || (s != 1 && s != 2) {
			return nil, errors.New("bm: slot must be 1 or 2")
		}
		n.slot = s
	}
	if v, ok := o["colour_code"]; ok {
		if c, err := strconv.Atoi(v); err != nil || c < 0 || c > 15 {
			return nil, errors.New("bm: colour_code must be 0..15")
		}
	}
	if v := o["super_header"]; strings.EqualFold(v, "false") || v == "0" {
		n.options &^= optSuperHeader
	}
	for key, dst := range map[string]*time.Duration{
		"keepalive_ms": &n.keepAlive, "timeout_ms": &n.timeout, "login_timeout_ms": &n.loginTO,
		"backoff_min_ms": &n.bMin, "backoff_max_ms": &n.bMax, "rx_timeout_ms": &n.rxTO,
	} {
		if v, ok := o[key]; ok {
			ms, err := strconv.Atoi(v)
			if err != nil || ms <= 0 {
				return nil, fmt.Errorf("bm: %s must be a positive integer", key)
			}
			*dst = time.Duration(ms) * time.Millisecond
		}
	}
	if n.bMax < n.bMin {
		n.bMax = n.bMin
	}
	return n, nil
}

func (n *Net) Mode() string         { return ctl.ModeBM }
func (n *Net) Codec() network.Codec { return network.CodecAMBE }

// Status reports the login state and the subscription.
func (n *Net) Status() ctl.NetStatus {
	n.mu.Lock()
	defer n.mu.Unlock()
	return ctl.NetStatus{Mode: ctl.ModeBM, Available: true, Enabled: n.enabled, Login: n.login, Detail: n.detail}
}

func nextBackoff(cur, max time.Duration) time.Duration {
	cur *= 2
	if cur > max {
		cur = max
	}
	return cur
}

func (n *Net) detailLocked(err error) string {
	d := ""
	if err != nil {
		d = err.Error()
	}
	if n.sub != nil {
		t := "TG"
		if n.sub.private {
			t = "private"
		}
		if d != "" {
			d += "; "
		}
		d += fmt.Sprintf("%s %d", t, n.sub.tg)
	}
	return d
}

func (n *Net) setLogin(st ctl.LoginState, err error) {
	n.mu.Lock()
	n.login, n.detail = st, n.detailLocked(err)
	s := n.sink
	d := n.detail
	n.mu.Unlock()
	if s != nil {
		s.Login(st, d)
	}
}

// Run logs in and keeps the session alive until ctx ends.
func (n *Net) Run(ctx context.Context, s network.Sink) error {
	n.mu.Lock()
	if n.running {
		n.mu.Unlock()
		return errors.New("bm: already running")
	}
	n.running, n.sink = true, s
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		n.running, n.logged = false, false
		n.mu.Unlock()
		n.setLogin(ctl.LoginOff, nil)
	}()
	backoff := n.bMin
	for ctx.Err() == nil {
		n.setLogin(ctl.LoginPending, nil)
		ok, err := n.session(ctx, s)
		if ctx.Err() != nil {
			break
		}
		if ok {
			backoff = n.bMin
		}
		if errors.Is(err, errBadPassword) {
			n.setLogin(ctl.LoginFailed, err)
		} else {
			n.setLogin(ctl.LoginPending, err)
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
		case <-t.C:
		}
		backoff = nextBackoff(backoff, n.bMax)
	}
	return nil
}

func (n *Net) sendLocked(typ, flags uint16, payload []byte, copies int) error {
	if n.conn == nil {
		return errors.New("bm: not connected")
	}
	idx := flags & flagRealTime1
	b := marshal(typ, flags, n.seq[idx], payload)
	n.seq[idx]++
	var err error
	for i := 0; i < copies; i++ {
		_, err = n.conn.Write(b)
	}
	return err
}

func (n *Net) send(typ, flags uint16, payload []byte) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.sendLocked(typ, flags, payload, 1)
}

func (n *Net) sendKeepAlive() error {
	return n.send(typKeepAlive, 0, versionData(n.clientID, n.version))
}

type rxState struct {
	active  bool
	call    uint64
	src     uint32
	dst     uint32
	private bool
	seq     uint32
	last    time.Time
	pending *network.CallHeader // voice header seen, waiting for super header / audio
}

func (n *Net) session(ctx context.Context, s network.Sink) (loggedIn bool, err error) {
	raddr, err := net.ResolveUDPAddr("udp", n.addr)
	if err != nil {
		return false, err
	}
	conn, err := net.DialUDP("udp", nil, raddr)
	if err != nil {
		return false, err
	}
	n.mu.Lock()
	n.conn, n.seq = conn, [2]uint32{}
	n.mu.Unlock()
	var rx rxState
	defer func() {
		if rx.active {
			s.End(rx.call)
		}
		n.mu.Lock()
		n.conn, n.logged, n.txActive, n.txBuf = nil, false, false, nil
		n.mu.Unlock()
		conn.Close()
	}()

	ch := make(chan packet, 64)
	go func() {
		defer close(ch)
		buf := make([]byte, maxPacket)
		for {
			m, err := conn.Read(buf)
			if err != nil {
				return
			}
			if p, err := unmarshal(buf[:m]); err == nil {
				select {
				case ch <- p:
				default:
				}
			}
		}
	}()

	tick := minDur(n.keepAlive, n.timeout, n.loginTO, n.rxTO) / 5
	if tick < time.Millisecond {
		tick = time.Millisecond
	}
	if tick > 100*time.Millisecond {
		tick = 100 * time.Millisecond
	}
	tk := time.NewTicker(tick)
	defer tk.Stop()

	start := time.Now()
	lastRx, lastTx := start, start
	retry := n.loginTO / 5
	authSent := 0
	if err := n.sendKeepAlive(); err != nil {
		return false, err
	}
	for {
		select {
		case <-ctx.Done():
			n.send(typClose, 0, nil)
			return loggedIn, nil
		case p, ok := <-ch:
			if !ok {
				return loggedIn, errLost
			}
			now := time.Now()
			lastRx = now
			switch p.typ {
			case typChallenge:
				if loggedIn {
					return true, errors.New("re-challenged")
				}
				if authSent >= 2 {
					return false, errBadPassword
				}
				d := sha256.Sum256(append(append([]byte(nil), p.payload...), n.password...))
				n.send(typAuth, 0, d[:])
				authSent++
			case typClose:
				if !loggedIn {
					return false, errBadPassword
				}
				return true, errors.New("closed by server")
			case typKeepAlive:
				if !loggedIn && authSent > 0 {
					n.send(typConfig, 0, u32s(n.options))
				}
			case typConfig:
				if !loggedIn {
					loggedIn = true
					n.mu.Lock()
					n.logged = true
					sub := n.sub
					n.mu.Unlock()
					n.setLogin(ctl.LoginOK, nil)
					if sub != nil {
						n.send(typSubscription, 0, u32s(sub.typ, sub.target))
					}
				}
			case typSuperHeader, typVoiceHeader, typTerminator, typAudio:
				if loggedIn {
					n.rxPacket(s, &rx, p, now)
				}
			}
		case <-tk.C:
			now := time.Now()
			if !loggedIn {
				if now.Sub(start) > n.loginTO {
					return false, errLoginTO
				}
				if now.Sub(lastTx) >= retry {
					n.sendKeepAlive()
					lastTx = now
				}
				continue
			}
			if now.Sub(lastRx) > n.timeout {
				return true, errLost
			}
			if now.Sub(lastTx) >= n.keepAlive {
				n.sendKeepAlive()
				lastTx = now
			}
			if rx.active && now.Sub(rx.last) > n.rxTO {
				s.End(rx.call)
				rx = rxState{}
			}
		}
	}
}

func minDur(d ...time.Duration) time.Duration {
	m := d[0]
	for _, x := range d[1:] {
		if x < m {
			m = x
		}
	}
	return m
}

func (n *Net) newCall() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nextCall++
	return n.nextCall
}

// startRX emits the header of a received call.
func (n *Net) startRX(s network.Sink, rx *rxState, h network.CallHeader, now time.Time) {
	if rx.active {
		s.End(rx.call)
	}
	h.Call = n.newCall()
	h.Slot = n.slot
	*rx = rxState{active: true, call: h.Call, src: h.SrcID, dst: h.TG, private: h.Private, last: now}
	s.Header(h)
}

func (n *Net) rxPacket(s network.Sink, rx *rxState, p packet, now time.Time) {
	switch p.typ {
	case typSuperHeader:
		sh, ok := parseSuper(p.payload)
		if !ok {
			return
		}
		h := network.CallHeader{Src: sh.srcCall, SrcID: sh.src, TG: sh.dst, Private: sh.typ == sessionPrivate}
		switch {
		case rx.pending != nil:
			// LC gives IDs, super header adds callsigns.
			rx.pending = nil
			n.startRX(s, rx, h, now)
		case rx.active && rx.src == sh.src && rx.dst == sh.dst:
			rx.last = now // repeat
		default:
			n.startRX(s, rx, h, now)
		}
	case typVoiceHeader:
		priv, dst, src, ok := parseLC(p.payload)
		if !ok {
			return
		}
		if rx.active && rx.src == src && rx.dst == dst {
			rx.last = now
			return
		}
		if rx.pending != nil {
			return
		}
		rx.pending = &network.CallHeader{SrcID: src, TG: dst, Private: priv}
		rx.last = now
	case typTerminator:
		if rx.pending != nil {
			n.startRX(s, rx, *rx.pending, now)
			rx.pending = nil
		}
		if rx.active {
			s.End(rx.call)
			*rx = rxState{}
		}
	case typAudio:
		if len(p.payload) == mode33Len {
			n.mu.Lock()
			n.dropped++
			n.mu.Unlock()
			return
		}
		if len(p.payload) != frameLen3 {
			return
		}
		if rx.pending != nil {
			n.startRX(s, rx, *rx.pending, now)
			rx.pending = nil
		}
		if !rx.active {
			return
		}
		rx.last = now
		for i := 0; i < 3; i++ {
			s.Voice(network.VoiceFrame{Call: rx.call, Seq: rx.seq, Data: p.payload[i*frameLen : (i+1)*frameLen]})
			rx.seq++
		}
	}
}

// Tune subscribes to a talkgroup or sets up a private target.
func (n *Net) Tune(ctx context.Context, t ctl.Target) error {
	if t.TG == 0 {
		return errors.New("bm: tune needs a talkgroup")
	}
	sub := &subscription{typ: sessionGroup, target: t.TG, tg: t.TG}
	if t.Private || t.TG == ParrotTG {
		sub.private, sub.typ, sub.target = true, sessionPrivate, n.id.DMRID
	}
	n.mu.Lock()
	old := n.sub
	n.sub = sub
	n.detail = n.detailLocked(nil)
	if n.logged {
		if old != nil {
			n.sendLocked(typCancelling, 0, u32s(old.typ, old.target), 1)
		}
		n.sendLocked(typSubscription, 0, u32s(sub.typ, sub.target), 1)
	}
	n.mu.Unlock()
	return nil
}

// Unlink cancels the subscription.
func (n *Net) Unlink(ctx context.Context) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sub = nil
	n.detail = n.detailLocked(nil)
	if n.logged {
		return n.sendLocked(typCancelling, 0, nil, 1)
	}
	return nil
}

// StartTX sends the super header (three times, same sequence number).
func (n *Net) StartTX(h network.CallHeader) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.logged {
		return errors.New("bm: not logged in")
	}
	dst, private := h.TG, h.Private
	if dst == 0 && n.sub != nil {
		dst, private = n.sub.tg, n.sub.private
	}
	if dst == 0 {
		return errors.New("bm: no target to transmit to")
	}
	src := h.SrcID
	if src == 0 {
		src = n.id.DMRID
	}
	call := h.Src
	if call == "" {
		call = n.id.Callsign
	}
	sh := superHeader{typ: sessionGroup, src: src, dst: dst, srcCall: call}
	if private || dst == ParrotTG {
		sh.typ = sessionPrivate
	}
	n.txCall, n.txActive, n.txBuf, n.txDst = h.Call, true, n.txBuf[:0], dst
	return n.sendLocked(typSuperHeader, flagRealTime1, sh.marshal(), 3)
}

// WriteVoice collects frames and sends every third as one audio packet.
func (n *Net) WriteVoice(f network.VoiceFrame) error {
	if len(f.Data) != frameLen {
		return fmt.Errorf("bm: frame must be %d bytes", frameLen)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.txActive || f.Call != n.txCall {
		return nil
	}
	n.txBuf = append(n.txBuf, f.Data...)
	if len(n.txBuf) < frameLen3 {
		return nil
	}
	err := n.sendLocked(typAudio, flagRealTime1, n.txBuf[:frameLen3], 1)
	n.txBuf = n.txBuf[:0]
	return err
}

// EndTX flushes a partial packet (padded with copies of the last frame) and
// sends the terminator three times.
func (n *Net) EndTX(call uint64) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	if !n.txActive || call != n.txCall {
		return nil
	}
	n.txActive = false
	if len(n.txBuf) > 0 {
		last := n.txBuf[len(n.txBuf)-frameLen:]
		for len(n.txBuf) < frameLen3 {
			n.txBuf = append(n.txBuf, last...)
		}
		n.sendLocked(typAudio, flagRealTime1, n.txBuf, 1)
		n.txBuf = n.txBuf[:0]
	}
	return n.sendLocked(typTerminator, flagRealTime1, nil, 3)
}
