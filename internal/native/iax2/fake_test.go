package iax2

import (
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// fakeClock is a manual clock. Advance moves time in ticker steps and
// delivers each tick before moving on.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	tickers []*fakeTicker
}

type fakeTicker struct {
	clk     *fakeClock
	c       chan time.Time
	d       time.Duration
	next    time.Time
	stopped bool
}

func newFakeClock() *fakeClock { return &fakeClock{now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)} }

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) NewTicker(d time.Duration) Ticker {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTicker{clk: f, c: make(chan time.Time), d: d, next: f.now.Add(d)}
	f.tickers = append(f.tickers, t)
	return t
}

func (t *fakeTicker) C() <-chan time.Time { return t.c }
func (t *fakeTicker) Stop() {
	t.clk.mu.Lock()
	t.stopped = true
	t.clk.mu.Unlock()
}

// Advance moves the clock forward by d, firing tickers on the way. A tick
// no one receives within a second is skipped (the call has ended).
func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	end := f.now.Add(d)
	f.mu.Unlock()
	for {
		f.mu.Lock()
		var due *fakeTicker
		for _, t := range f.tickers {
			if !t.stopped && !t.next.After(end) && (due == nil || t.next.Before(due.next)) {
				due = t
			}
		}
		if due == nil {
			f.now = end
			f.mu.Unlock()
			return
		}
		f.now = due.next
		due.next = due.next.Add(due.d)
		at := f.now
		f.mu.Unlock()
		select {
		case due.c <- at:
		case <-time.After(time.Second):
			f.mu.Lock()
			due.stopped = true
			f.mu.Unlock()
		}
	}
}

// fakePeer is a scripted IAX2 peer on loopback. In auto mode it ACKs
// counted frames and answers PING and LAGRQ, as Asterisk does, and passes
// every frame it receives to the test.
type fakePeer struct {
	t    *testing.T
	conn *net.UDPConn
	addr netip.AddrPort

	// wmu keeps sequence numbering and sending in one order, so frames
	// from the reader and the test never reach the client swapped.
	wmu sync.Mutex

	mu       sync.Mutex
	client   netip.AddrPort
	call     uint16 // our (peer) call number
	remote   uint16 // client's call number
	oseq     uint8
	iseq     uint8
	auto     bool
	start    time.Time
	received chan rxFrame
}

type rxFrame struct {
	full *FullFrame
	mini *MiniFrame
}

func newFakePeer(t *testing.T) *fakePeer {
	t.Helper()
	c, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Skipf("cannot bind loopback: %v", err)
	}
	p := &fakePeer{t: t, conn: c, addr: c.LocalAddr().(*net.UDPAddr).AddrPort(), call: 7,
		start: time.Now(), received: make(chan rxFrame, 1024)}
	t.Cleanup(func() { c.Close() })
	go p.read()
	return p
}

func (p *fakePeer) read() {
	for {
		buf := make([]byte, 1500)
		n, from, err := p.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			return
		}
		full, mini, err := Decode(buf[:n])
		if err != nil {
			continue
		}
		p.mu.Lock()
		p.client = from
		auto := p.auto
		if full != nil && auto && countsForSeq(full.Type, full.Subclass) {
			if full.OSeq == p.iseq {
				p.iseq++
			}
		}
		p.mu.Unlock()
		if full != nil && auto && countsForSeq(full.Type, full.Subclass) {
			switch {
			case full.Type == TypeIAX && full.Subclass == CmdPing:
				p.sendTS(TypeIAX, CmdPong, full.TS, nil)
			case full.Type == TypeIAX && full.Subclass == CmdLagRq:
				p.sendTS(TypeIAX, CmdLagRp, full.TS, nil)
			default:
				p.ack(full)
			}
		}
		p.received <- rxFrame{full, mini}
	}
}

func (p *fakePeer) setAuto(on bool) {
	p.mu.Lock()
	p.auto = on
	p.mu.Unlock()
}

// acceptNew records the client's NEW: from here the peer's seqs follow it.
func (p *fakePeer) acceptNew(f *FullFrame) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.remote = f.SrcCall
	p.iseq = f.OSeq + 1
	p.oseq = 0
}

func (p *fakePeer) raw(f FullFrame) {
	b, err := f.Append(nil)
	if err != nil {
		panic(err) // a test bug; t.Error may not be called after the test ends
	}
	p.mu.Lock()
	to := p.client
	p.mu.Unlock()
	// Write errors are ignored: the socket closes when the test ends,
	// while the reader may still be acknowledging a last frame.
	_, _ = p.conn.WriteToUDPAddrPort(b, to)
}

func (p *fakePeer) ts() uint32 { return uint32(time.Since(p.start).Milliseconds()) }

func (p *fakePeer) send(t FrameType, sc uint32, data []byte) { p.sendTS(t, sc, p.ts(), data) }

func (p *fakePeer) sendTS(t FrameType, sc uint32, ts uint32, data []byte) {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	p.mu.Lock()
	f := FullFrame{SrcCall: p.call, DstCall: p.remote, TS: ts, OSeq: p.oseq, ISeq: p.iseq,
		Type: t, Subclass: sc, Data: data}
	if countsForSeq(t, sc) {
		p.oseq++
	}
	p.mu.Unlock()
	p.raw(f)
}

func (p *fakePeer) sendIEs(sc uint32, ies IEs) {
	b, err := ies.Encode()
	if err != nil {
		p.t.Fatal(err)
	}
	p.send(TypeIAX, sc, b)
}

func (p *fakePeer) ack(f *FullFrame) {
	p.mu.Lock()
	a := FullFrame{SrcCall: p.call, DstCall: f.SrcCall, TS: f.TS, OSeq: p.oseq, ISeq: p.iseq,
		Type: TypeIAX, Subclass: CmdAck}
	p.mu.Unlock()
	p.raw(a)
}

func (p *fakePeer) mini(ts uint16, data []byte) {
	m := MiniFrame{SrcCall: p.call, TS: ts, Data: data}
	p.mu.Lock()
	to := p.client
	p.mu.Unlock()
	p.conn.WriteToUDPAddrPort(m.Append(nil), to)
}

// expect waits for a full frame of type t and subclass sc, skipping others.
func (p *fakePeer) expect(t FrameType, sc uint32) *FullFrame {
	p.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-p.received:
			if r.full != nil && r.full.Type == t && r.full.Subclass == sc {
				return r.full
			}
		case <-deadline:
			p.t.Fatalf("peer: no frame type %d subclass %d", t, sc)
			return nil
		}
	}
}

// expectMini waits for a mini frame, skipping full frames.
func (p *fakePeer) expectMini() *MiniFrame {
	p.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-p.received:
			if r.mini != nil {
				return r.mini
			}
		case <-deadline:
			p.t.Fatal("peer: no mini frame")
			return nil
		}
	}
}

// expectText waits for a TEXT frame with the given text.
func (p *fakePeer) expectText(text string) {
	p.t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case r := <-p.received:
			if r.full != nil && r.full.Type == TypeText && string(r.full.Data) == text+"\x00" {
				return
			}
		case <-deadline:
			p.t.Fatalf("peer: no text %q", text)
		}
	}
}
