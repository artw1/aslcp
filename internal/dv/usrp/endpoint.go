package usrp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// FrameInterval is the voice frame period and the tx pacer tick.
	FrameInterval = 20 * time.Millisecond
	// TxQueueMax is the tx queue cap in voice frames (1 s). When full the
	// oldest frame is dropped.
	TxQueueMax = 50
	rxQueue    = 64
)

// Stats are endpoint counters.
type Stats struct {
	RxFrames  uint64    // voice frames accepted from the peer
	TxFrames  uint64    // voice frames sent to the peer
	RxDropped uint64    // rx queue full
	TxDropped uint64    // tx queue overflow
	Foreign   uint64    // datagrams from other sources or malformed
	LastRx    time.Time // last accepted packet
}

type txItem struct {
	pcm *[Samples]int16 // nil: end of transmission (keyup=0)
}

// Endpoint is one UDP socket speaking USRP with a single peer: it receives
// on the listen address and sends to the peer address. Only datagrams whose
// source IP equals the peer IP are accepted.
type Endpoint struct {
	conn   *net.UDPConn
	peer   *net.UDPAddr
	peerIP netip.Addr

	rx  chan Packet
	seq atomic.Uint32

	mu    sync.Mutex
	queue []txItem

	rxFrames, txFrames, rxDropped, txDropped, foreign atomic.Uint64
	lastRx                                            atomic.Int64

	closeOnce sync.Once
	done      chan struct{}
	wg        sync.WaitGroup
}

// Listen binds the listen address and starts the read loop and the tx pacer.
// Close stops them.
func Listen(listen, peer string) (*Endpoint, error) {
	la, err := net.ResolveUDPAddr("udp", listen)
	if err != nil {
		return nil, fmt.Errorf("usrp listen %q: %w", listen, err)
	}
	pa, err := net.ResolveUDPAddr("udp", peer)
	if err != nil {
		return nil, fmt.Errorf("usrp peer %q: %w", peer, err)
	}
	pip, ok := netip.AddrFromSlice(pa.IP)
	if !ok {
		return nil, fmt.Errorf("usrp peer %q: no IP", peer)
	}
	conn, err := net.ListenUDP("udp", la)
	if err != nil {
		return nil, err
	}
	e := &Endpoint{
		conn:   conn,
		peer:   pa,
		peerIP: pip.Unmap(),
		rx:     make(chan Packet, rxQueue),
		done:   make(chan struct{}),
	}
	e.wg.Add(2)
	go e.readLoop()
	go e.pacer()
	return e, nil
}

// Addr is the bound listen address.
func (e *Endpoint) Addr() net.Addr { return e.conn.LocalAddr() }

// Rx delivers decoded packets from the peer. It is never closed.
func (e *Endpoint) Rx() <-chan Packet { return e.rx }

// Close stops the endpoint and waits for its goroutines.
func (e *Endpoint) Close() error {
	var err error
	e.closeOnce.Do(func() {
		close(e.done)
		err = e.conn.Close()
		e.wg.Wait()
	})
	return err
}

// Stats returns a snapshot of the counters.
func (e *Endpoint) Stats() Stats {
	s := Stats{
		RxFrames:  e.rxFrames.Load(),
		TxFrames:  e.txFrames.Load(),
		RxDropped: e.rxDropped.Load(),
		TxDropped: e.txDropped.Load(),
		Foreign:   e.foreign.Load(),
	}
	if ns := e.lastRx.Load(); ns != 0 {
		s.LastRx = time.Unix(0, ns)
	}
	return s
}

func (e *Endpoint) readLoop() {
	defer e.wg.Done()
	buf := make([]byte, MaxPacket+64)
	for {
		n, from, err := e.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			select {
			case <-e.done:
				return
			case <-time.After(10 * time.Millisecond):
			}
			continue
		}
		if from.Addr().Unmap() != e.peerIP {
			e.foreign.Add(1)
			continue
		}
		p, err := Parse(buf[:n])
		if err != nil {
			e.foreign.Add(1)
			continue
		}
		e.lastRx.Store(time.Now().UnixNano())
		if p.Type == TypeVoice && p.PCM != nil {
			e.rxFrames.Add(1)
		}
		select {
		case e.rx <- p:
		default:
			e.rxDropped.Add(1)
		}
	}
}

// SendVoice queues one voice frame for the pacer. If the queue holds
// TxQueueMax frames the oldest is dropped.
func (e *Endpoint) SendVoice(pcm [Samples]int16) {
	e.mu.Lock()
	defer e.mu.Unlock()
	voice := 0
	for _, it := range e.queue {
		if it.pcm != nil {
			voice++
		}
	}
	if voice >= TxQueueMax {
		for i, it := range e.queue {
			if it.pcm != nil {
				e.queue = append(e.queue[:i], e.queue[i+1:]...)
				e.txDropped.Add(1)
				break
			}
		}
	}
	e.queue = append(e.queue, txItem{pcm: &pcm})
}

// EndTX queues the keyup=0 header; it is sent after all queued voice.
func (e *Endpoint) EndTX() {
	e.mu.Lock()
	e.queue = append(e.queue, txItem{})
	e.mu.Unlock()
}

// Queued is the number of items waiting for the pacer.
func (e *Endpoint) Queued() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.queue)
}

// SendText sends a TEXT packet immediately.
func (e *Endpoint) SendText(tlv []byte) error {
	p := Packet{Seq: e.seq.Add(1) - 1, Type: TypeText, Text: tlv}
	return e.write(p.Append(nil))
}

func (e *Endpoint) write(b []byte) error {
	_, err := e.conn.WriteToUDP(b, e.peer)
	return err
}

func (e *Endpoint) pacer() {
	defer e.wg.Done()
	t := time.NewTicker(FrameInterval)
	defer t.Stop()
	var buf []byte
	for {
		select {
		case <-e.done:
			return
		case <-t.C:
		}
		e.mu.Lock()
		var it txItem
		ok := len(e.queue) > 0
		if ok {
			it = e.queue[0]
			e.queue = e.queue[1:]
		}
		e.mu.Unlock()
		if !ok {
			continue
		}
		p := Packet{Seq: e.seq.Add(1) - 1, Type: TypeVoice}
		if it.pcm != nil {
			p.Keyup = true
			p.PCM = it.pcm
		}
		buf = p.Append(buf[:0])
		if e.write(buf) == nil && it.pcm != nil {
			e.txFrames.Add(1)
		}
	}
}

// Wait blocks until ctx ends and then closes the endpoint.
func (e *Endpoint) Wait(ctx context.Context) {
	<-ctx.Done()
	e.Close()
}
