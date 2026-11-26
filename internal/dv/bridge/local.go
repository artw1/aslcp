package bridge

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/artw1/aslcp/internal/dv/usrp"
)

// LocalEndpoint is an in-process audio endpoint for the router: it implements
// the USRP interface without a socket, so a host process (the native node)
// can attach the DV engine to its mixer as one leg, with no Asterisk, no
// chan_usrp and no node number.
//
// Direction names follow the router: what the host pushes is the node's
// transmission (the router encodes it and sends it to the DV network); what
// the router sends back is received DV audio, which the host pulls once per
// 20 ms frame tick. The host does the pacing, so LocalEndpoint runs no
// goroutines.
type LocalEndpoint struct {
	rx chan usrp.Packet
	// keyed: the host's leg toward the router is keyed (Push since Unkey).
	keyed atomic.Bool

	mu     sync.Mutex
	queue  []localItem // router -> host
	active bool     // router is sending (voice since the last end marker)
	meta   string   // last metadata text from the router ("N0CALL 91")

	rxFrames, txFrames, rxDropped, txDropped atomic.Uint64
	lastRx                                   atomic.Int64
}

// localItem is one queued frame of received audio; nil pcm marks the end
// of a call.
type localItem struct{ pcm *[usrp.Samples]int16 }

// localRxQueue bounds the host -> router packets waiting for the router loop.
const localRxQueue = 64

// NewLocalEndpoint returns an idle in-process endpoint.
func NewLocalEndpoint() *LocalEndpoint {
	return &LocalEndpoint{rx: make(chan usrp.Packet, localRxQueue)}
}

// Rx implements USRP: the packets the host pushed.
func (l *LocalEndpoint) Rx() <-chan usrp.Packet { return l.rx }

// Push hands one frame of the node's audio to the router (keyup voice). It
// never blocks; a full queue drops the frame and counts it.
func (l *LocalEndpoint) Push(pcm *[usrp.Samples]int16) {
	f := *pcm
	l.keyed.Store(true)
	l.lastRx.Store(time.Now().UnixNano())
	select {
	case l.rx <- usrp.Packet{Type: usrp.TypeVoice, Keyup: true, PCM: &f}:
		l.rxFrames.Add(1)
	default:
		l.rxDropped.Add(1)
	}
}

// Unkey ends the node's transmission toward the router (a bare keyup=0
// header). It does nothing when the leg is not keyed.
func (l *LocalEndpoint) Unkey() {
	if !l.keyed.Swap(false) {
		return
	}
	select {
	case l.rx <- usrp.Packet{Type: usrp.TypeVoice}:
	default:
		// The router's silence timeout ends the call anyway.
		l.rxDropped.Add(1)
	}
}

// Keyed reports whether the host's leg toward the router is keyed.
func (l *LocalEndpoint) Keyed() bool { return l.keyed.Load() }

// SendVoice implements USRP: one frame of received DV audio for the host. If
// TxQueueMax frames wait, the oldest is dropped.
func (l *LocalEndpoint) SendVoice(pcm [usrp.Samples]int16) {
	l.mu.Lock()
	defer l.mu.Unlock()
	voice := 0
	for _, it := range l.queue {
		if it.pcm != nil {
			voice++
		}
	}
	if voice >= usrp.TxQueueMax {
		for i, it := range l.queue {
			if it.pcm != nil {
				l.queue = append(l.queue[:i], l.queue[i+1:]...)
				l.txDropped.Add(1)
				break
			}
		}
	}
	l.queue = append(l.queue, localItem{pcm: &pcm})
	l.active = true
}

// EndTX implements USRP: the end of the received call, after its queued voice.
func (l *LocalEndpoint) EndTX() {
	l.mu.Lock()
	l.queue = append(l.queue, localItem{})
	l.mu.Unlock()
}

// SendText implements USRP: the router's call metadata ("callsign room").
func (l *LocalEndpoint) SendText(tlv []byte) error {
	if len(tlv) >= 2 && tlv[0] == usrp.TLVSetInfo {
		n := int(tlv[1])
		if n > len(tlv)-2 {
			n = len(tlv) - 2
		}
		l.mu.Lock()
		l.meta = string(tlv[2 : 2+n])
		l.mu.Unlock()
	}
	return nil
}

// Pull returns the next frame of received DV audio, at most one per call;
// the host calls it once per frame tick. ok is false when no frame is due
// (idle, or the end of a call was reached).
func (l *LocalEndpoint) Pull() (pcm [usrp.Samples]int16, ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.queue) == 0 {
		return pcm, false
	}
	it := l.queue[0]
	l.queue = l.queue[1:]
	if it.pcm == nil {
		l.active = false
		l.meta = ""
		return pcm, false
	}
	l.txFrames.Add(1)
	return *it.pcm, true
}

// Active reports whether the router is sending a received call (from its
// first frame until the end marker has been pulled).
func (l *LocalEndpoint) Active() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active
}

// Talker is the metadata of the received call in progress ("" if none).
func (l *LocalEndpoint) Talker() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.active {
		return ""
	}
	return l.meta
}

// Stats implements USRP. RxFrames counts frames the host pushed, TxFrames
// frames the host pulled.
func (l *LocalEndpoint) Stats() usrp.Stats {
	s := usrp.Stats{
		RxFrames:  l.rxFrames.Load(),
		TxFrames:  l.txFrames.Load(),
		RxDropped: l.rxDropped.Load(),
		TxDropped: l.txDropped.Load(),
	}
	if ns := l.lastRx.Load(); ns != 0 {
		s.LastRx = time.Unix(0, ns)
	}
	return s
}
