// Package bridge is the call state machine between the USRP endpoint, the
// vocoder and the active network.
package bridge

import (
	"context"
	"fmt"
	"log"
	"math"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/artw1/aslcp/internal/dv/ctl"
	"github.com/artw1/aslcp/internal/dv/imbe"
	"github.com/artw1/aslcp/internal/dv/network"
	"github.com/artw1/aslcp/internal/dv/usrp"
	"github.com/artw1/aslcp/internal/dv/vocoder"
)

const (
	// DefaultTxTimeout ends a transmission when USRP frames stop without a
	// keyup=0.
	DefaultTxTimeout = 300 * time.Millisecond
	// DefaultRxSilence ends a received call when the network goes quiet
	// without a terminator.
	DefaultRxSilence = 1500 * time.Millisecond

	// DefaultLoginWait is how long entering an on-demand network waits for
	// its login. The BrandMeister client tries every 5 s and backs off 1 s
	// between attempts, so this covers two attempts.
	DefaultLoginWait = 12 * time.Second

	// stopWait bounds the wait for a network's goroutine to end after its
	// session was cancelled (the close packet goes out first).
	stopWait = 2 * time.Second

	tick     = 100 * time.Millisecond
	workQ    = 64
	netEvQ   = 1024
	metaMax  = 64
	cmdQueue = 16
)

// USRP is the endpoint as the router uses it (*usrp.Endpoint implements it).
type USRP interface {
	Rx() <-chan usrp.Packet
	SendVoice(pcm [usrp.Samples]int16)
	EndTX()
	SendText(tlv []byte) error
	Stats() usrp.Stats
}

// Options configures a Router.
type Options struct {
	USRP USRP
	AMBE vocoder.AMBE
	// NewIMBE builds one IMBE codec (stateful, single goroutine); the
	// router makes one per direction and call. nil: imbe.NewCodec.
	NewIMBE func() vocoder.IMBE
	// NewCodec2 builds one Codec2 3200 codec, like NewIMBE. nil: vocoder.NewCodec2.
	NewCodec2 func() vocoder.Codec2
	Config    ctl.Config
	Version   string
	// VocoderStats reports the md380 client counters (optional).
	VocoderStats func() vocoder.Stats
	// Emit receives every event (optional). It must not block.
	Emit func(ctl.Event)
	// Reload re-reads the config file for the reload op. An error is
	// reported as bad_config and the old config stays.
	Reload func() (ctl.Config, error)
	// InitialMode selects the network at start ("" = none).
	InitialMode string
	TxTimeout   time.Duration
	RxSilence   time.Duration
	// LoginWait bounds the wait for the login of an on-demand network when
	// it is entered. 0: DefaultLoginWait.
	LoginWait time.Duration
	Log       *log.Logger
}

type netEntry struct {
	cfg    ctl.NetConfig
	n      network.Network // nil when not implemented in this build
	cancel context.CancelFunc
	login  ctl.LoginState
	detail string

	// On demand (ctl.NetConfig.OnDemand): the session runs only while the
	// network is the active mode. running tells whether one is up; used,
	// that n has run before (a new session gets a new network, so nothing
	// of the old one, such as its subscription, carries over). gen numbers
	// the sessions: events of an older one are dropped. done closes when
	// the network's goroutine ended. waiters are entries waiting for the
	// login.
	onDemand bool
	running  bool
	used     bool
	gen      uint64
	done     chan struct{}
	waiters  []chan error
}

type netEvent struct {
	e      *netEntry
	gen    uint64
	hdr    *network.CallHeader
	frame  *network.VoiceFrame
	end    uint64
	isEnd  bool
	login  ctl.LoginState
	detail string
	isLog  bool
}

type job struct {
	call  uint64
	seq   uint32
	codec network.Codec // which of ambe / imbe / c2 is valid
	pcm   [vocoder.PCMSamples]int16
	ambe  [vocoder.AMBEBytes]byte
	imbe  [vocoder.IMBEBytes]byte
	c2    [vocoder.C2Bytes]byte
	end   bool
}

// codecLen is the frame length of codec c.
func codecLen(c network.Codec) int {
	switch c {
	case network.CodecIMBE:
		return vocoder.IMBEBytes
	case network.CodecC2:
		return vocoder.C2Bytes
	}
	return vocoder.AMBEBytes
}

// imbeState is the IMBE codec of one direction. It is owned by that
// direction's worker goroutine and replaced at every new call, because the
// codec is stateful and must not be shared between directions.
type imbeState struct {
	newf func() vocoder.IMBE
	call uint64
	c    vocoder.IMBE
}

func (s *imbeState) get(call uint64) vocoder.IMBE {
	if s.c == nil || s.call != call {
		s.c, s.call = s.newf(), call
	}
	return s.c
}

func (s *imbeState) encode(call uint64, pcm *[vocoder.PCMSamples]int16) (out [vocoder.IMBEBytes]byte) {
	defer func() {
		if recover() != nil {
			s.c = nil
			out = silenceIMBE
		}
	}()
	return s.get(call).Encode(pcm)
}

func (s *imbeState) decode(call uint64, f *[vocoder.IMBEBytes]byte) (out [vocoder.PCMSamples]int16) {
	defer func() {
		if recover() != nil {
			s.c = nil
			out = [vocoder.PCMSamples]int16{}
		}
	}()
	return s.get(call).Decode(f)
}

// silenceIMBE is the frame that replaces one whose encode failed: the
// encoding of digital silence, computed once.
var silenceIMBE = func() [vocoder.IMBEBytes]byte {
	var pcm [vocoder.PCMSamples]int16
	return imbe.NewCodec().Encode(&pcm)
}()

// c2State is imbeState for Codec2.
type c2State struct {
	newf func() vocoder.Codec2
	call uint64
	c    vocoder.Codec2
}

func (s *c2State) get(call uint64) vocoder.Codec2 {
	if s.c == nil || s.call != call {
		s.c, s.call = s.newf(), call
	}
	return s.c
}

func (s *c2State) encode(call uint64, pcm *[vocoder.PCMSamples]int16) (out [vocoder.C2Bytes]byte) {
	defer func() {
		if recover() != nil {
			s.c = nil
			out = silenceC2
		}
	}()
	return s.get(call).Encode(pcm)
}

func (s *c2State) decode(call uint64, f *[vocoder.C2Bytes]byte) (out [vocoder.PCMSamples]int16) {
	defer func() {
		if recover() != nil {
			s.c = nil
			out = [vocoder.PCMSamples]int16{}
		}
	}()
	return s.get(call).Decode(f)
}

// silenceC2 is the encoding of digital silence by a fresh encoder.
var silenceC2 = func() [vocoder.C2Bytes]byte {
	var pcm [vocoder.PCMSamples]int16
	return vocoder.NewCodec2().Encode(&pcm)
}()

type call struct {
	talk    ctl.Talk
	net     *netEntry
	netCall uint64 // rx: the network's call id
	last    time.Time
	seq     uint32
	ending  bool
}

// Router owns all call state; everything runs on the goroutine of Run.
type Router struct {
	o    Options
	log  *log.Logger
	cmds chan func()
	netq chan netEvent
	enc  chan job
	dec  chan job
	encO chan job
	decO chan job

	stopped chan struct{}
	runCtx  context.Context
	wg      sync.WaitGroup

	// state, owned by Run
	cfg     ctl.Config
	started time.Time
	updated time.Time
	mode    string
	target  *ctl.Target
	nets    []*netEntry
	nextID  uint64
	tx, rx  *call
	vocDown bool
	dropped atomic.Uint64
	// audio: the AGC and the level meter of each direction (docs/DV.md "Audio level and AGC")
	audTx, audRx *audioPath
}

// New builds a Router; call Run to start it.
func New(o Options) *Router {
	if o.TxTimeout <= 0 {
		o.TxTimeout = DefaultTxTimeout
	}
	if o.RxSilence <= 0 {
		o.RxSilence = DefaultRxSilence
	}
	if o.LoginWait <= 0 {
		o.LoginWait = DefaultLoginWait
	}
	if o.Log == nil {
		o.Log = log.New(discard{}, "", 0)
	}
	if o.NewIMBE == nil {
		o.NewIMBE = func() vocoder.IMBE { return imbe.NewCodec() }
	}
	if o.NewCodec2 == nil {
		o.NewCodec2 = func() vocoder.Codec2 { return vocoder.NewCodec2() }
	}
	return &Router{
		o: o, log: o.Log, cfg: o.Config,
		cmds: make(chan func(), cmdQueue), netq: make(chan netEvent, netEvQ),
		enc: make(chan job, workQ), dec: make(chan job, workQ),
		encO: make(chan job, workQ), decO: make(chan job, workQ),
		stopped: make(chan struct{}),
		audTx:   newAudioPath(), audRx: newAudioPath(),
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// Run drives the router until ctx ends. If a call is active, TX gets its
// keyup=0 before Run returns.
func (r *Router) Run(ctx context.Context) error {
	defer close(r.stopped)
	r.runCtx = ctx
	now := time.Now()
	r.started, r.updated = now, now
	r.startNets()
	if m := r.o.InitialMode; m != "" {
		if e := r.find(m); e != nil && e.n != nil && e.cfg.Enabled {
			r.mode = m
			r.startSession(e) // an on-demand network logs in now; the others already run
		}
	}
	// Separate IMBE state per direction: each worker goroutine owns its own.
	encIMBE := &imbeState{newf: r.o.NewIMBE}
	decIMBE := &imbeState{newf: r.o.NewIMBE}
	encC2 := &c2State{newf: r.o.NewCodec2}
	decC2 := &c2State{newf: r.o.NewCodec2}
	r.worker(ctx, r.enc, r.encO, func(j job) job {
		pcm := j.pcm
		switch j.codec {
		case network.CodecIMBE:
			j.imbe = encIMBE.encode(j.call, &pcm)
		case network.CodecC2:
			j.c2 = encC2.encode(j.call, &pcm)
		default:
			j.ambe, _ = r.o.AMBE.Encode(ctx, &pcm)
		}
		return j
	})
	r.worker(ctx, r.dec, r.decO, func(j job) job {
		switch j.codec {
		case network.CodecIMBE:
			j.pcm = decIMBE.decode(j.call, &j.imbe)
		case network.CodecC2:
			j.pcm = decC2.decode(j.call, &j.c2)
		default:
			j.pcm, _ = r.o.AMBE.Decode(ctx, j.ambe)
		}
		return j
	})
	t := time.NewTicker(tick)
	defer t.Stop()
	rx := r.o.USRP.Rx()
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		case p := <-rx:
			r.onUSRP(p)
		case ev := <-r.netq:
			r.onNet(ev)
		case j := <-r.encO:
			r.onEncoded(j)
		case j := <-r.decO:
			r.onDecoded(j)
		case f := <-r.cmds:
			f()
		case <-t.C:
			r.housekeeping()
		}
	}
	// Shutdown: close open calls without waiting for the pipelines.
	if r.tx != nil {
		r.finishTX()
	}
	if r.rx != nil {
		r.finishRX()
	}
	r.o.USRP.EndTX()
	for _, e := range r.nets {
		if e.cancel != nil {
			e.cancel()
		}
	}
	r.wg.Wait()
	return nil
}

func (r *Router) worker(ctx context.Context, in <-chan job, out chan<- job, f func(job) job) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case j := <-in:
				if !j.end {
					j = f(j)
				}
				select {
				case out <- j:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
}

func (r *Router) emit(ev ctl.Event) {
	if r.o.Emit == nil {
		return
	}
	if ev.At.IsZero() {
		ev.At = time.Now()
	}
	r.o.Emit(ev)
}

func (r *Router) emitStatus() {
	st := r.status()
	r.emit(ctl.Event{Type: ctl.EvStatus, Status: &st})
}

func (r *Router) emitTalk(typ string, t ctl.Talk) {
	r.emit(ctl.Event{Type: typ, Talk: &t})
}

// ---- networks ----

func (r *Router) find(mode string) *netEntry {
	for _, e := range r.nets {
		if e.cfg.Mode == mode {
			return e
		}
	}
	return nil
}

type sink struct {
	r   *Router
	e   *netEntry
	gen uint64
}

func (s sink) push(ev netEvent) {
	ev.e, ev.gen = s.e, s.gen
	select {
	case s.r.netq <- ev:
	default:
		s.r.dropped.Add(1)
	}
}
func (s sink) Header(h network.CallHeader) { s.push(netEvent{hdr: &h}) }
func (s sink) Voice(f network.VoiceFrame) {
	f.Data = append([]byte(nil), f.Data...)
	s.push(netEvent{frame: &f})
}
func (s sink) End(c uint64) { s.push(netEvent{isEnd: true, end: c}) }
func (s sink) Login(st ctl.LoginState, d string) {
	s.push(netEvent{isLog: true, login: st, detail: d})
}

func (r *Router) newEntry(c ctl.NetConfig) *netEntry {
	e := &netEntry{cfg: c, login: ctl.LoginOff, onDemand: c.OnDemand != nil && *c.OnDemand}
	if !network.Available(c.Mode) {
		return e
	}
	n, err := network.New(c, r.cfg.Identity)
	if err != nil {
		// a network that is switched off and not set up is not an error
		if c.Enabled {
			r.log.Printf("network %s: %v", c.Mode, err)
			e.detail = err.Error()
		}
		return e
	}
	e.n = n
	switch {
	case !c.Enabled:
	case e.onDemand:
		e.login = ctl.LoginIdle
	default:
		r.startSession(e)
	}
	return e
}

// startSession starts the network's session (its Run goroutine) unless one is
// up. An on-demand network gets a new network value for every session. It
// runs on the router goroutine.
func (r *Router) startSession(e *netEntry) {
	if e.running || e.n == nil || !e.cfg.Enabled {
		return
	}
	if e.used {
		n, err := network.New(e.cfg, r.cfg.Identity)
		if err != nil {
			r.log.Printf("network %s: %v", e.cfg.Mode, err)
			e.detail = err.Error()
			return
		}
		e.n = n
	}
	e.used, e.running = true, true
	e.gen++
	// Only a network with a login starts out "pending". YSF, P25, NXDN and M17
	// have none: they stay "off" until a reflector is tuned, and their client
	// never re-announces an "off" it already holds, so a "pending" set here would
	// stick for good.
	e.login, e.detail = ctl.LoginOff, ""
	if hasLogin(e.cfg.Mode) {
		e.login = ctl.LoginPending
	}
	ctx, cancel := context.WithCancel(r.runCtx)
	done := make(chan struct{})
	e.cancel, e.done = cancel, done
	n, sk := e.n, sink{r, e, e.gen}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		defer close(done)
		if err := n.Run(ctx, sk); err != nil && ctx.Err() == nil {
			r.log.Printf("network %s stopped: %v", e.cfg.Mode, err)
		}
	}()
}

// hasLogin reports whether the network of mode logs in to a master (the DMR
// networks); YSF, P25, NXDN and M17 only link to a reflector or talkgroup.
func hasLogin(mode string) bool {
	return mode == ctl.ModeBM || mode == ctl.ModeTGIF || mode == ctl.ModeDMR
}

// stopSession ends the network's session: the context is cancelled (BM sends
// its close packet first) and the goroutine given stopWait to finish. Late
// events of the old session are dropped. An on-demand network reads "idle"
// afterwards. It runs on the router goroutine.
func (r *Router) stopSession(e *netEntry) {
	if !e.running {
		return
	}
	e.cancel()
	select {
	case <-e.done:
	case <-time.After(stopWait):
		r.log.Printf("network %s: session did not end in %v", e.cfg.Mode, stopWait)
	}
	e.running, e.cancel = false, nil
	e.gen++
	e.login, e.detail = ctl.LoginIdle, ""
	r.emit(ctl.Event{Type: ctl.EvLogin, Login: &ctl.LoginEvent{Mode: e.cfg.Mode, State: ctl.LoginIdle}})
}

// leave ends the use of e as the active network: it is unlinked, and an
// on-demand one then logs out.
func (r *Router) leave(e *netEntry) {
	e.n.Unlink(r.runCtx)
	if e.onDemand {
		r.stopSession(e)
	}
}

// loginWait registers a wait for the login of e and returns its channel; nil
// when nothing is to be waited for (e is not on demand, or logged in).
func (r *Router) loginWait(e *netEntry) <-chan error {
	if e == nil || !e.onDemand || e.login == ctl.LoginOK || !e.running {
		return nil
	}
	ch := make(chan error, 1)
	e.waiters = append(e.waiters, ch)
	return ch
}

func (e *netEntry) dropWaiter(ch <-chan error) {
	for i, w := range e.waiters {
		if (<-chan error)(w) == ch {
			e.waiters = append(e.waiters[:i], e.waiters[i+1:]...)
			return
		}
	}
}

// netName is how an error message names a mode.
func netName(mode string) string {
	if mode == ctl.ModeBM {
		return "BrandMeister"
	}
	return mode
}

// awaitLogin waits, off the router goroutine so the router keeps serving
// audio, events and status, for the login of e. When the master refuses it,
// does not answer within LoginWait or the caller gives up, the mode is left
// again (the session is stopped) and the error says why.
func (r *Router) awaitLogin(ctx context.Context, e *netEntry, ch <-chan error) error {
	wait := r.o.LoginWait
	t := time.NewTimer(wait)
	defer t.Stop()
	var err error
	timedOut := false
	select {
	case err = <-ch:
	case <-t.C:
		timedOut = true
	case <-ctx.Done():
		err = ctx.Err()
	case <-r.stopped:
		return cerr(ctl.CodeInternal, "engine stopping")
	}
	if err == nil && !timedOut {
		return nil
	}
	var out error
	actx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if xerr := r.exec(actx, func() {
		e.dropWaiter(ch)
		if e.login == ctl.LoginOK && timedOut {
			return // logged in just now
		}
		if timedOut {
			d := ""
			if e.detail != "" {
				d = " (" + e.detail + ")"
			}
			err = cerr(ctl.CodeLoginFailed, "%s did not answer the login within %s%s", netName(e.cfg.Mode), wait.Round(time.Second), d)
		}
		out = err
		if r.active() == e {
			r.cutCalls()
			r.mode, r.target = ctl.ModeNone, nil
			r.updated = time.Now()
		}
		r.stopSession(e)
		r.emitStatus()
	}); xerr != nil {
		return xerr
	}
	return out
}

func (r *Router) startNets() {
	r.nets = nil
	for _, c := range r.cfg.Networks {
		r.nets = append(r.nets, r.newEntry(c))
	}
}

// ---- USRP side (node transmits) ----

func (r *Router) active() *netEntry {
	if r.mode == ctl.ModeNone {
		return nil
	}
	e := r.find(r.mode)
	if e == nil || e.n == nil || !e.cfg.Enabled {
		return nil
	}
	return e
}

func (r *Router) onUSRP(p usrp.Packet) {
	switch p.Type {
	case usrp.TypeDTMF:
		r.log.Printf("usrp dtmf %q (ignored in this build)", p.DTMF)
		return
	case usrp.TypeText:
		r.log.Printf("usrp text %d bytes (ignored)", len(p.Text))
		return
	}
	now := time.Now()
	if !p.Keyup {
		if r.tx != nil && !r.tx.ending {
			r.endTX()
		}
		return
	}
	if p.PCM == nil {
		return
	}
	if r.tx == nil {
		if !r.startTX(now) {
			return
		}
	}
	if r.tx.ending {
		return
	}
	r.tx.last = now
	j := job{call: r.tx.talk.ID, seq: r.tx.seq, codec: r.tx.net.n.Codec()}
	r.tx.seq++
	j.pcm = *p.PCM
	applyGain(&j.pcm, r.cfg.USRP.GainRx)
	r.audTx.process(now, &j.pcm, r.cfg.USRP.AGCTx, r.agcParams())
	select {
	case r.enc <- j:
	default:
		r.dropped.Add(1)
	}
}

func (r *Router) startTX(now time.Time) bool {
	e := r.active()
	if e == nil {
		return false
	}
	if r.rx != nil {
		// The node's transmission wins over a received call.
		r.cutRX()
	}
	r.nextID++
	id := r.nextID
	t := ctl.Talk{ID: id, Dir: "tx", Mode: r.mode, Call: r.cfg.Identity.Callsign,
		DMRID: r.cfg.Identity.DMRID, Start: now}
	h := network.CallHeader{Call: id, Src: t.Call, SrcID: t.DMRID}
	if r.target != nil {
		t.TG, t.Room, t.Slot, t.Private = r.target.TG, r.target.Room, r.target.Slot, r.target.Private
		h.TG, h.Room, h.Slot, h.Private = t.TG, t.Room, t.Slot, t.Private
	}
	if err := e.n.StartTX(h); err != nil {
		r.log.Printf("start tx: %v", err)
		return false
	}
	r.tx = &call{talk: t, net: e, last: now}
	r.audTx.begin()
	r.updated = now
	r.emitTalk(ctl.EvTalkStart, t)
	r.emitStatus()
	return true
}

// endTX queues the end marker behind the frames already in the encoder.
func (r *Router) endTX() {
	r.tx.ending = true
	select {
	case r.enc <- job{call: r.tx.talk.ID, end: true}:
	default:
		r.finishTX()
	}
}

func (r *Router) onEncoded(j job) {
	if r.tx == nil || r.tx.talk.ID != j.call {
		return
	}
	if j.end {
		r.finishTX()
		return
	}
	f := network.VoiceFrame{Call: j.call, Seq: j.seq, Codec: j.codec, Data: j.ambe[:]}
	switch j.codec {
	case network.CodecIMBE:
		f.Data = j.imbe[:]
	case network.CodecC2:
		f.Data = j.c2[:]
	}
	if err := r.tx.net.n.WriteVoice(f); err != nil {
		r.log.Printf("write voice: %v", err)
	}
}

func (r *Router) finishTX() {
	c := r.tx
	r.tx = nil
	c.talk.End = time.Now()
	c.talk.Duration = c.talk.End.Sub(c.talk.Start)
	r.updated = c.talk.End
	r.audTx.end(c.talk.End, c.talk.Duration, r.agcTarget())
	r.emitTalk(ctl.EvTalkEnd, c.talk)
	if err := c.net.n.EndTX(c.talk.ID); err != nil {
		r.log.Printf("end tx: %v", err)
	}
	r.emitStatus()
}

// ---- network side (node receives) ----

func (r *Router) onNet(ev netEvent) {
	e := ev.e
	if ev.gen != e.gen {
		return // of a session that was stopped
	}
	if ev.isLog {
		e.login, e.detail = ev.login, ev.detail
		switch ev.login {
		case ctl.LoginOK:
			r.wake(e, nil)
		case ctl.LoginFailed:
			d := ""
			if ev.detail != "" {
				d = ": " + ev.detail
			}
			r.wake(e, cerr(ctl.CodeLoginFailed, "%s refused the login%s", netName(e.cfg.Mode), d))
		}
		r.emit(ctl.Event{Type: ctl.EvLogin, Login: &ctl.LoginEvent{Mode: e.cfg.Mode, State: ev.login, Detail: ev.detail}})
		return
	}
	if r.active() != e {
		return
	}
	now := time.Now()
	switch {
	case ev.hdr != nil:
		if r.tx != nil || r.rx != nil {
			return
		}
		h := ev.hdr
		r.nextID++
		t := ctl.Talk{ID: r.nextID, Dir: "rx", Mode: e.cfg.Mode, Call: h.Src, DMRID: h.SrcID,
			TG: h.TG, Room: h.Room, Slot: h.Slot, Private: h.Private, Start: now}
		r.rx = &call{talk: t, net: e, netCall: h.Call, last: now}
		r.audRx.begin()
		r.updated = now
		r.emitTalk(ctl.EvTalkStart, t)
		r.emitStatus()
		meta := h.Src
		if h.Room != "" {
			meta += " " + h.Room
		} else if h.TG != 0 {
			meta += fmt.Sprintf(" %d", h.TG)
		}
		if err := r.o.USRP.SendText(usrp.MetaTLV(meta)); err != nil {
			r.log.Printf("send text: %v", err)
		}
	case ev.frame != nil:
		c := r.rx
		if c == nil || c.ending || c.netCall != ev.frame.Call {
			return
		}
		fc := ev.frame.Codec
		if (fc != network.CodecAMBE && fc != network.CodecIMBE && fc != network.CodecC2) || len(ev.frame.Data) != codecLen(fc) {
			// wrong length for its codec (or unknown codec): drop and count
			r.dropped.Add(1)
			return
		}
		c.last = now
		j := job{call: c.talk.ID, seq: ev.frame.Seq, codec: fc}
		switch fc {
		case network.CodecIMBE:
			copy(j.imbe[:], ev.frame.Data)
		case network.CodecC2:
			copy(j.c2[:], ev.frame.Data)
		default:
			copy(j.ambe[:], ev.frame.Data)
		}
		select {
		case r.dec <- j:
		default:
			r.dropped.Add(1)
		}
	case ev.isEnd:
		if c := r.rx; c != nil && !c.ending && c.netCall == ev.end {
			r.endRX()
		}
	}
}

// wake answers the entries waiting for the login of e.
func (r *Router) wake(e *netEntry, err error) {
	for _, w := range e.waiters {
		w <- err // buffered 1, one answer per waiter
	}
	e.waiters = nil
}

func (r *Router) endRX() {
	r.rx.ending = true
	select {
	case r.dec <- job{call: r.rx.talk.ID, end: true}:
	default:
		r.finishRX()
	}
}

func (r *Router) onDecoded(j job) {
	if r.rx == nil || r.rx.talk.ID != j.call {
		return
	}
	if j.end {
		r.finishRX()
		return
	}
	applyGain(&j.pcm, r.cfg.USRP.GainTx)
	r.audRx.process(time.Now(), &j.pcm, r.cfg.USRP.AGCRx, r.agcParams())
	r.o.USRP.SendVoice(j.pcm)
}

func (r *Router) finishRX() {
	c := r.rx
	r.rx = nil
	c.talk.End = time.Now()
	c.talk.Duration = c.talk.End.Sub(c.talk.Start)
	r.updated = c.talk.End
	r.o.USRP.EndTX()
	r.audRx.end(c.talk.End, c.talk.Duration, r.agcTarget())
	r.emitTalk(ctl.EvTalkEnd, c.talk)
	r.emitStatus()
}

// cutRX ends the received call immediately; queued audio of it is dropped by
// the call-id check.
func (r *Router) cutRX() { r.finishRX() }

func (r *Router) housekeeping() {
	now := time.Now()
	if c := r.tx; c != nil && !c.ending && now.Sub(c.last) > r.o.TxTimeout {
		r.endTX()
	}
	if c := r.rx; c != nil && !c.ending && now.Sub(c.last) > r.o.RxSilence {
		r.endRX()
	}
	if r.o.VocoderStats != nil {
		s := r.o.VocoderStats()
		if s.Down != r.vocDown {
			r.vocDown = s.Down
			v := r.vocStatus()
			r.emit(ctl.Event{Type: ctl.EvVocoder, Vocoder: &v})
		}
	}
}

func (r *Router) vocStatus() ctl.VocoderStatus {
	v := ctl.VocoderStatus{OK: true, Addr: r.cfg.Vocoder.MD380}
	if r.o.VocoderStats != nil {
		s := r.o.VocoderStats()
		v.OK, v.Frames, v.Timeouts = !s.Down, s.Frames, s.Timeouts
	}
	return v
}

// ---- status ----

func (r *Router) status() ctl.Status {
	us := r.o.USRP.Stats()
	st := ctl.Status{
		Version: r.o.Version, Started: r.started, Mode: r.mode, Updated: r.updated,
		USRP: ctl.USRPStatus{Listen: r.cfg.USRP.Listen, Peer: r.cfg.USRP.Peer,
			LastRx: us.LastRx, RxFrames: us.RxFrames, TxFrames: us.TxFrames},
		Vocoder:  r.vocStatus(),
		Networks: []ctl.NetStatus{},
	}
	if r.target != nil {
		t := *r.target
		st.Target = &t
	}
	for _, e := range r.nets {
		// Available: this build implements the mode, whether or not its
		// settings are complete (a misconfigured network reports why in Detail).
		ns := ctl.NetStatus{Mode: e.cfg.Mode, Available: network.Available(e.cfg.Mode), Enabled: e.cfg.Enabled, Login: e.login, Detail: e.detail}
		if e.n != nil && (e.running || !e.onDemand) {
			if s := e.n.Status(); s.Detail != "" && ns.Detail == "" {
				ns.Detail = s.Detail
			}
		}
		st.Networks = append(st.Networks, ns)
	}
	now := time.Now()
	st.Audio = &ctl.AudioStatus{TargetDb: r.agcTarget(),
		Tx: r.audTx.m.snapshot(now, r.cfg.USRP.AGCTx), Rx: r.audRx.m.snapshot(now, r.cfg.USRP.AGCRx)}
	if c := r.tx; c != nil {
		t := c.talk
		st.Talk = &t
	} else if c := r.rx; c != nil {
		t := c.talk
		st.Talk = &t
	}
	return st
}

// ---- ctl handler ----

// exec runs f on the router goroutine.
func (r *Router) exec(ctx context.Context, f func()) error {
	done := make(chan struct{})
	wrapped := func() { f(); close(done) }
	select {
	case r.cmds <- wrapped:
	case <-r.stopped:
		return &ctl.Error{Code: ctl.CodeInternal, Msg: "engine stopping"}
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-done:
		return nil
	case <-r.stopped:
		return &ctl.Error{Code: ctl.CodeInternal, Msg: "engine stopping"}
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Router) do(ctx context.Context, f func() error) (ctl.Status, error) {
	var st ctl.Status
	var ferr error
	if err := r.exec(ctx, func() {
		ferr = f()
		st = r.status()
	}); err != nil {
		return st, err
	}
	return st, ferr
}

func cerr(code, format string, a ...any) error {
	return &ctl.Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Status returns the current status.
func (r *Router) Status(ctx context.Context) (ctl.Status, error) {
	return r.do(ctx, func() error { return nil })
}

func (r *Router) callActive() bool { return r.tx != nil || r.rx != nil }

func (r *Router) checkMode(mode string) (*netEntry, error) {
	e := r.find(mode)
	switch {
	case e == nil && network.Available(mode):
		return nil, cerr(ctl.CodeNotEnabled, "network %s is not configured", mode)
	case e != nil && e.n == nil && network.Available(mode):
		return nil, cerr(ctl.CodeNotEnabled, "network %s is not configured: %s", mode, e.detail)
	case e == nil || e.n == nil:
		return nil, cerr(ctl.CodeNotAvailable, "network %s is not available in this build", mode)
	case !e.cfg.Enabled:
		return nil, cerr(ctl.CodeNotEnabled, "network %s is disabled", mode)
	}
	return e, nil
}

func (r *Router) cutCalls() {
	if r.tx != nil {
		r.finishTX()
	}
	if r.rx != nil {
		r.finishRX()
	}
}

func (r *Router) switchMode(mode string, force bool) (*netEntry, error) {
	var e *netEntry
	if mode != ctl.ModeNone {
		var err error
		if e, err = r.checkMode(mode); err != nil {
			return nil, err
		}
	}
	if mode == r.mode {
		if e != nil && e.onDemand {
			r.startSession(e)
		}
		return e, nil
	}
	if r.callActive() {
		if !force {
			return nil, cerr(ctl.CodeBusy, "a call is in progress")
		}
		r.cutCalls()
	}
	if old := r.active(); old != nil {
		r.leave(old)
	}
	r.mode, r.target = mode, nil
	r.updated = time.Now()
	if e != nil && e.onDemand {
		r.startSession(e)
	}
	return e, nil
}

// Mode switches the active network. Entering an on-demand network starts its
// session and waits for the login (LoginWait); a refused or unanswered login
// leaves the mode again and returns CodeLoginFailed.
func (r *Router) Mode(ctx context.Context, a ctl.ModeArgs) (ctl.Status, error) {
	var (
		e    *netEntry
		wait <-chan error
	)
	st, err := r.do(ctx, func() error {
		ent, err := r.switchMode(a.Mode, a.Force)
		if err != nil {
			return err
		}
		e, wait = ent, r.loginWait(ent)
		r.emitStatus()
		return nil
	})
	if err != nil || wait == nil {
		return st, err
	}
	if err := r.awaitLogin(ctx, e, wait); err != nil {
		st, _ := r.Status(context.Background())
		return st, err
	}
	return r.Status(ctx)
}

// Tune points the network at a target (switching mode if needed). Like Mode,
// it waits for the login of an on-demand network before it tunes.
func (r *Router) Tune(ctx context.Context, a ctl.TuneArgs) (ctl.Status, error) {
	var (
		e    *netEntry
		wait <-chan error
	)
	st, err := r.do(ctx, func() error {
		if a.Mode == ctl.ModeNone {
			return cerr(ctl.CodeBadRequest, "tune needs a mode")
		}
		ent, err := r.switchMode(a.Mode, a.Force)
		if err != nil {
			return err
		}
		e = ent
		if wait = r.loginWait(e); wait != nil {
			r.emitStatus() // the mode is in; the card shows "Logging in"
			return nil
		}
		return r.tuneActive(ctx, e, a.Target)
	})
	if err != nil || wait == nil {
		return st, err
	}
	if err := r.awaitLogin(ctx, e, wait); err != nil {
		st, _ := r.Status(context.Background())
		return st, err
	}
	return r.do(ctx, func() error {
		if r.active() != e {
			return cerr(ctl.CodeBusy, "the mode changed while %s was logging in", netName(a.Mode))
		}
		return r.tuneActive(ctx, e, a.Target)
	})
}

func (r *Router) tuneActive(ctx context.Context, e *netEntry, t ctl.Target) error {
	if err := e.n.Tune(ctx, t); err != nil {
		return cerr(ctl.CodeInternal, "tune: %v", err)
	}
	r.target = &t
	r.updated = time.Now()
	r.emitStatus()
	return nil
}

// Unlink disconnects the target of the current network. With a.Release the
// network is left like a mode switch leaves it (leave): an on-demand session
// ends too, and a call in progress is cut. The mode stays.
func (r *Router) Unlink(ctx context.Context, a ctl.UnlinkArgs) (ctl.Status, error) {
	return r.do(ctx, func() error {
		if a.Mode != ctl.ModeNone && a.Mode != r.mode {
			return cerr(ctl.CodeBadRequest, "mode %s is not the current mode", a.Mode)
		}
		if e := r.active(); e != nil && a.Release {
			r.cutCalls()
			r.leave(e)
		} else if e != nil {
			if err := e.n.Unlink(ctx); err != nil {
				return cerr(ctl.CodeInternal, "unlink: %v", err)
			}
		}
		r.target = nil
		r.updated = time.Now()
		r.emitStatus()
		return nil
	})
}

// Reload re-reads the config. A bad file leaves the running config in place.
func (r *Router) Reload(ctx context.Context) (ctl.Status, error) {
	if r.o.Reload == nil {
		return r.Status(ctx)
	}
	cfg, err := r.o.Reload()
	if err != nil {
		return ctl.Status{}, &ctl.Error{Code: ctl.CodeBadConfig, Msg: err.Error()}
	}
	return r.do(ctx, func() error { r.apply(cfg); return nil })
}

// apply swaps in a new config. Networks whose config changed are restarted;
// the USRP ports need a process restart and are only logged. The md380
// address and timeout are applied by the caller (Options.Reload hook).
func (r *Router) apply(cfg ctl.Config) {
	if cfg.USRP.Listen != r.cfg.USRP.Listen || cfg.USRP.Peer != r.cfg.USRP.Peer {
		r.log.Printf("config: usrp addresses changed; restart aslcp-dv to apply")
		cfg.USRP.Listen, cfg.USRP.Peer = r.cfg.USRP.Listen, r.cfg.USRP.Peer
	}
	identityChanged := !reflect.DeepEqual(cfg.Identity, r.cfg.Identity)
	old := r.nets
	r.cfg = cfg
	var nets []*netEntry
	for _, c := range cfg.Networks {
		var keep *netEntry
		for _, e := range old {
			if e.cfg.Mode == c.Mode && !identityChanged && reflect.DeepEqual(e.cfg, c) {
				keep = e
			}
		}
		if keep != nil {
			nets = append(nets, keep)
			continue
		}
		nets = append(nets, nil) // filled after the old ones stop
	}
	for _, e := range old {
		kept := false
		for _, n := range nets {
			if n == e {
				kept = true
			}
		}
		if !kept && e.cancel != nil {
			if e.cfg.Mode == r.mode {
				r.cutCalls()
				r.mode, r.target = ctl.ModeNone, nil
			}
			e.cancel()
			r.wake(e, cerr(ctl.CodeInternal, "the configuration changed"))
		}
	}
	for i, n := range nets {
		if n == nil {
			nets[i] = r.newEntry(cfg.Networks[i])
		}
	}
	r.nets = nets
	if r.mode != ctl.ModeNone && r.active() == nil {
		r.cutCalls()
		r.mode, r.target = ctl.ModeNone, nil
	}
	r.updated = time.Now()
	r.emitStatus()
}

// agcTarget is the configured target level (dBFS); 0, which no valid file
// has, reads as the default.
func (r *Router) agcTarget() int {
	if t := r.cfg.USRP.AGCTarget; t != 0 {
		return t
	}
	return ctl.DefaultAGCTarget
}

func (r *Router) agcParams() AGCParams {
	return AGCParams{TargetDb: float64(r.agcTarget()), MaxGainDb: float64(r.cfg.USRP.AGCMax)}
}

func applyGain(p *[vocoder.PCMSamples]int16, db int) {
	if db == 0 {
		return
	}
	g := math.Pow(10, float64(db)/20)
	for i, s := range p {
		v := math.Round(float64(s) * g)
		if v > math.MaxInt16 {
			v = math.MaxInt16
		} else if v < math.MinInt16 {
			v = math.MinInt16
		}
		p[i] = int16(v)
	}
}
