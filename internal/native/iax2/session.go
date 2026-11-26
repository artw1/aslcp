package iax2

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"time"
)

type callState int

const (
	stateDialing   callState = iota // NEW sent, waiting for ACCEPT
	stateAccepted                   // ACCEPT seen, waiting for ANSWER
	stateUp                         // answered
	stateHangingUp                  // our HANGUP sent, waiting for its ACK
)

// pending is a sent full frame awaiting acknowledgment.
type pending struct {
	f       FullFrame
	sentAt  time.Time
	nextAt  time.Time
	rto     time.Duration
	retries int
}

// session is the call state owned by the loop goroutine.
type session struct {
	c     *Call
	state callState
	start time.Time

	peerCall   uint16
	oseq, iseq uint8
	lastSentTS uint32
	pend       []*pending
	rtt        time.Duration
	token      []byte
	tokenTries int

	dialDeadline   time.Time
	hangupDeadline time.Time
	lastRx         time.Time
	nextPing       time.Time
	nextLag        time.Time

	// receive side
	lastRxTS     uint32 // latest full or reconstructed voice timestamp
	jb           *JitterBuffer
	rxKeyed      bool
	lastVoiceAt  time.Time
	lastKeyCtlAt time.Time
	redundant    bool // peer uses !NEWKEY!: keying by RADIO_KEY/UNKEY

	// transmit side
	txOn          bool
	quelched      bool
	nextTxAt      time.Time
	voiceTS       uint32
	voiceFull     bool // next voice frame must be a full frame
	nextKeyResend time.Time

	links        []LinkEntry
	nextLinkList time.Time

	// pendingErr is what the call ends with once our HANGUP is through.
	pendingErr error
}

func (s *session) init(c *Call) {
	s.c = c
	now := c.clock.Now()
	s.start = now
	s.dialDeadline = now.Add(c.cfg.DialTimeout)
	s.lastRx = now
	s.jb = NewJitterBuffer(c.cfg.Jitter)
}

// loop runs the call until it ends.
func (c *Call) loop(t Ticker) {
	defer t.Stop()
	s := &c.s
	s.sendNew()
	for {
		select {
		case in := <-c.inbound:
			if in.full != nil {
				s.handleFull(in.full, in.at)
			} else {
				s.handleMini(in.mini, in.at)
			}
		case cmd := <-c.cmds:
			s.handleCmd(cmd)
		case <-t.C():
			s.tick(c.clock.Now())
		case <-c.done:
			return
		}
		select {
		case <-c.done:
			return
		default:
		}
	}
}

// finish ends the call with err (nil for a clean local hangup).
func (s *session) finish(err error) {
	c := s.c
	c.errOnce.Do(func() {
		c.err = err
		c.mu.Lock()
		c.closed = true
		c.txq = nil
		c.rxKey = false
		c.mu.Unlock()
		close(c.done)
		_ = c.conn.Close()
		close(c.recv)
		close(c.events)
	})
}

func (s *session) ended() bool {
	select {
	case <-s.c.done:
		return true
	default:
		return false
	}
}

// callTS is the timestamp for a non-voice full frame: milliseconds since
// the call started, strictly increasing.
func (s *session) callTS(now time.Time) uint32 {
	ts := uint32(now.Sub(s.start).Milliseconds())
	if ts <= s.lastSentTS {
		ts = s.lastSentTS + 1
	}
	return ts
}

func (s *session) write(b []byte) {
	_, _ = s.c.conn.WriteToUDPAddrPort(b, s.c.peer)
}

// sendFull sends a full frame. Frames that advance the sequence are queued
// for retransmission until acknowledged.
func (s *session) sendFull(t FrameType, sc uint32, ts uint32, data []byte) {
	now := s.c.clock.Now()
	f := FullFrame{
		SrcCall: s.c.localCall, DstCall: s.peerCall, TS: ts,
		OSeq: s.oseq, ISeq: s.iseq, Type: t, Subclass: sc, Data: data,
	}
	b, err := f.Append(nil)
	if err != nil {
		return
	}
	if ts > s.lastSentTS {
		s.lastSentTS = ts
	}
	if countsForSeq(t, sc) {
		s.oseq++
		rto := s.rto()
		s.pend = append(s.pend, &pending{f: f, sentAt: now, nextAt: now.Add(rto), rto: rto})
	}
	s.write(b)
}

func (s *session) sendIAX(sc uint32, ies IEs) {
	data, err := ies.Encode()
	if err != nil {
		return
	}
	s.sendFull(TypeIAX, sc, s.callTS(s.c.clock.Now()), data)
}

func (s *session) sendControl(sc uint32) {
	s.sendFull(TypeControl, sc, s.callTS(s.c.clock.Now()), nil)
}

func (s *session) sendText(text string) {
	s.sendFull(TypeText, 0, s.callTS(s.c.clock.Now()), append([]byte(text), 0))
}

// ack acknowledges a received full frame, echoing its timestamp.
func (s *session) ack(f *FullFrame) {
	a := FullFrame{
		SrcCall: s.c.localCall, DstCall: f.SrcCall, TS: f.TS,
		OSeq: s.oseq, ISeq: s.iseq, Type: TypeIAX, Subclass: CmdAck,
	}
	if b, err := a.Append(nil); err == nil {
		s.write(b)
	}
}

func (s *session) rto() time.Duration {
	if s.rtt <= 0 {
		return initialRTO
	}
	d := 2 * s.rtt
	if d < minRTO {
		d = minRTO
	}
	if d > maxRTO/4 {
		d = maxRTO / 4
	}
	return d
}

// sendNew sends NEW, carrying the call token once the peer has given one.
func (s *session) sendNew() {
	cfg := &s.c.cfg
	now := s.c.clock.Now()
	var ies IEs
	ies.AddU16(IEVersion, ProtocolVersion)
	ies.AddString(IECalledNumber, cfg.RemoteNode)
	ies.AddString(IECallingNumber, cfg.LocalNode)
	ies.AddU8(IECallingPres, 0)
	ies.AddU8(IECallingTON, 0)
	ies.AddU16(IECallingTNS, 0)
	if cfg.Callsign != "" {
		ies.AddString(IECallingName, cfg.Callsign)
	}
	ies.AddString(IEUsername, cfg.Username)
	ies.AddU32(IEFormat, FormatULaw)
	ies.AddFormat2(IEFormat2, uint64(FormatULaw))
	ies.AddU32(IECapability, FormatULaw)
	ies.AddFormat2(IECapability2, uint64(FormatULaw))
	ies.AddU16(IEADSICPE, 0)
	ies.AddU32(IEDateTime, PackDateTime(now))
	ies.AddBytes(IECallToken, s.token)
	s.sendIAX(CmdNew, ies)
}

// ackUpTo drops pending frames acknowledged by a peer iseq.
func (s *session) ackUpTo(iseq uint8, now time.Time) {
	kept := s.pend[:0]
	for _, p := range s.pend {
		if d := iseq - p.f.OSeq; d >= 1 && d <= 128 {
			if p.retries == 0 {
				s.sampleRTT(now.Sub(p.sentAt))
			}
			continue
		}
		kept = append(kept, p)
	}
	s.pend = kept
}

func (s *session) sampleRTT(d time.Duration) {
	if s.rtt == 0 {
		s.rtt = d
	} else {
		s.rtt = (7*s.rtt + d) / 8
	}
	s.c.count(func(st *Stats) { st.RTT = s.rtt })
}

func (s *session) handleFull(f *FullFrame, now time.Time) {
	if s.ended() {
		return
	}
	if f.DstCall != s.c.localCall {
		s.c.count(func(st *Stats) { st.Foreign++ })
		return
	}
	// Apathetic replies come from a peer that holds no call state yet:
	// CALLTOKEN, and REJECT before the call exists. They carry no usable
	// sequence numbers or source call number.
	if f.Type == TypeIAX && f.Subclass == CmdCallToken {
		s.handleCallToken(f)
		return
	}
	if s.peerCall == 0 {
		if f.Type == TypeIAX && f.Subclass == CmdReject {
			s.reject(f)
			return
		}
		s.peerCall = f.SrcCall
	} else if f.SrcCall != s.peerCall {
		s.c.count(func(st *Stats) { st.Foreign++ })
		return
	}
	s.lastRx = now
	s.c.count(func(st *Stats) { st.LastRx = now })
	s.ackUpTo(f.ISeq, now)

	if countsForSeq(f.Type, f.Subclass) {
		switch d := f.OSeq - s.iseq; {
		case d == 0:
			s.iseq++
		case d >= 128:
			// Already seen: our ACK was lost. Ack again and drop it.
			s.ack(f)
			return
		default:
			// A gap: ask for everything from iseq again.
			s.sendFull(TypeIAX, CmdVNAK, s.callTS(now), nil)
			return
		}
	}
	switch f.Type {
	case TypeIAX:
		s.handleIAX(f, now)
	case TypeControl:
		s.ack(f)
		s.handleControl(f, now)
	case TypeVoice:
		s.ack(f)
		if f.Subclass != FormatULaw {
			return
		}
		s.lastRxTS = f.TS
		s.voice(f.TS, f.Data, now)
	case TypeText:
		s.ack(f)
		s.handleText(ParseLinkText(f.Data), now)
	default:
		// CNG, DTMF, NULL and anything else: acknowledged, not used.
		s.ack(f)
	}
}

func (s *session) handleCallToken(f *FullFrame) {
	if s.state != stateDialing {
		return
	}
	ies, err := ParseIEs(f.Data)
	if err != nil {
		return
	}
	tok, ok := ies.Get(IECallToken)
	if !ok || len(tok) == 0 {
		return
	}
	s.tokenTries++
	if s.tokenTries > 2 {
		s.finish(fmt.Errorf("%w: call token not accepted", ErrRejected))
		return
	}
	// Start over with the token: the peer keeps no state for the first NEW.
	s.token = append([]byte(nil), tok...)
	s.pend = nil
	s.oseq, s.iseq = 0, 0
	s.sendNew()
}

func (s *session) reject(f *FullFrame) {
	ies, _ := ParseIEs(f.Data)
	s.ack(f)
	cause := ies.String(IECause)
	s.finish(fmt.Errorf("%w by node %s: %s", ErrRejected, s.c.cfg.RemoteNode, cause))
}

func (s *session) handleIAX(f *FullFrame, now time.Time) {
	switch f.Subclass {
	case CmdAck:
		if s.state == stateHangingUp && len(s.pend) == 0 {
			s.finish(s.pendingErr)
		}
	case CmdReject:
		s.reject(f)
	case CmdHangup:
		ies, _ := ParseIEs(f.Data)
		s.ack(f)
		if s.state == stateHangingUp {
			s.finish(s.pendingErr)
			return
		}
		s.finish(fmt.Errorf("%w: %s", ErrHungUp, ies.String(IECause)))
	case CmdAuthReq:
		s.ack(f)
		s.authenticate(f)
	case CmdAccept:
		s.ack(f)
		if s.state != stateDialing {
			return
		}
		ies, _ := ParseIEs(f.Data)
		if fm, ok := ies.Format(); ok && fm&uint64(FormatULaw) == 0 {
			s.hangup(now, "Format not supported")
			s.finish(fmt.Errorf("%w: format 0x%x", ErrFormat, fm))
			return
		}
		s.state = stateAccepted
	case CmdPing:
		s.sendFull(TypeIAX, CmdPong, f.TS, nil)
	case CmdLagRq:
		s.sendFull(TypeIAX, CmdLagRp, f.TS, nil)
	case CmdPong:
		s.ack(f)
	case CmdLagRp:
		s.ack(f)
		lag := time.Duration(s.nowTS(now)-f.TS) * time.Millisecond
		s.c.count(func(st *Stats) { st.Lag = lag })
	case CmdInval:
		s.finish(fmt.Errorf("%w: peer invalidated the call", ErrHungUp))
	case CmdVNAK:
		for _, p := range s.pend {
			s.retransmit(p, now)
		}
	case CmdQuelch:
		s.ack(f)
		s.quelched = true
	case CmdUnquelch:
		s.ack(f)
		s.quelched = false
	default:
		s.ack(f)
	}
}

func (s *session) nowTS(now time.Time) uint32 {
	return uint32(now.Sub(s.start).Milliseconds())
}

// authenticate answers AUTHREQ with MD5 when offered, plaintext only when
// it is the only method the peer accepts.
func (s *session) authenticate(f *FullFrame) {
	ies, _ := ParseIEs(f.Data)
	methods, _ := ies.U16(IEAuthMethods)
	secret := s.c.cfg.Secret
	if secret == "" {
		s.hangup(s.c.clock.Now(), "No secret")
		s.finish(fmt.Errorf("%w: peer requires a secret and none is set", ErrAuth))
		return
	}
	var rep IEs
	if u := ies.String(IEUsername); u != "" {
		rep.AddString(IEUsername, u)
	}
	switch {
	case methods&AuthMD5 != 0:
		rep.AddString(IEMD5Result, MD5Response(ies.String(IEChallenge), secret))
	case methods&AuthPlaintext != 0:
		rep.AddString(IEPassword, secret)
	default:
		s.hangup(s.c.clock.Now(), "No usable auth method")
		s.finish(fmt.Errorf("%w: methods 0x%x", ErrAuth, methods))
		return
	}
	s.sendIAX(CmdAuthRep, rep)
}

// MD5Response is the IAX2 MD5 challenge answer: lowercase hex of
// MD5(challenge + secret).
func MD5Response(challenge, secret string) string {
	sum := md5.Sum([]byte(challenge + secret))
	return hex.EncodeToString(sum[:])
}

func (s *session) handleControl(f *FullFrame, now time.Time) {
	switch f.Subclass {
	case CtlAnswer:
		if s.state != stateDialing && s.state != stateAccepted {
			return
		}
		s.state = stateUp
		s.nextPing = now.Add(s.c.cfg.PingInterval)
		s.nextLag = now.Add(s.c.cfg.LagInterval)
		s.nextLinkList = now.Add(linkListShort)
		s.sendText(TextNewKey1)
		close(s.c.answered)
	case CtlHangup:
		s.finish(fmt.Errorf("%w: hangup control", ErrHungUp))
	case CtlBusy, CtlCongestion:
		s.finish(fmt.Errorf("%w: busy or congested", ErrRejected))
	case CtlRadioKey:
		if s.redundant {
			s.lastKeyCtlAt = now
			s.setRxKey(true)
		}
	case CtlRadioUnkey:
		s.setRxKey(false)
	}
}

func (s *session) handleText(t LinkText, now time.Time) {
	switch t.Kind {
	case TextKindNewKey1:
		s.redundant = false
	case TextKindNewKey:
		// An older peer keys with RADIO_KEY/UNKEY, repeated every 2 s.
		// app_rpt answers the first !NEWKEY! with its own.
		if !s.redundant {
			s.redundant = true
			s.sendText(TextNewKey)
			s.nextKeyResend = now.Add(redundantTxTime)
		}
	case TextKindIAXKey:
	case TextKindDisconnect:
		s.hangup(now, "Disconnect requested")
		s.state = stateHangingUp
		s.pendingErr = fmt.Errorf("%w by node %s", ErrDisconnected, s.c.cfg.RemoteNode)
	case TextKindLinkList:
		s.c.mu.Lock()
		changed := !equalLinks(s.c.links, t.Links)
		s.c.links = t.Links
		s.c.mu.Unlock()
		if changed {
			s.c.emit(Event{Kind: EventLinkList, Links: append([]LinkEntry(nil), t.Links...)})
		}
	default:
		s.c.emit(Event{Kind: EventText, Text: t})
	}
}

func equalLinks(a, b []LinkEntry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func (s *session) setRxKey(on bool) {
	if s.rxKeyed == on {
		return
	}
	s.rxKeyed = on
	s.c.mu.Lock()
	s.c.rxKey = on
	s.c.mu.Unlock()
	s.c.emit(Event{Kind: EventRxKey, Keyed: on})
}

func (s *session) handleMini(m *MiniFrame, now time.Time) {
	if s.ended() || s.peerCall == 0 || m.SrcCall != s.peerCall {
		s.c.count(func(st *Stats) { st.Foreign++ })
		return
	}
	if s.state != stateUp && s.state != stateAccepted {
		return
	}
	s.lastRx = now
	ts := s.lastRxTS&0xffff0000 | uint32(m.TS)
	if ts < s.lastRxTS && s.lastRxTS-ts > 0x8000 {
		ts += 0x10000
	} else if ts > s.lastRxTS && ts-s.lastRxTS > 0x8000 && ts >= 0x10000 {
		ts -= 0x10000 // a late frame from before the wrap
	}
	if ts > s.lastRxTS {
		s.lastRxTS = ts
	}
	s.voice(ts, m.Data, now)
}

// voice feeds a ulaw payload to the jitter buffer and tracks keying.
func (s *session) voice(ts uint32, data []byte, now time.Time) {
	frames := DecodeULaw(data)
	for i := range frames {
		s.jb.Put(int64(ts)+int64(i*frameMS), frames[i], now)
	}
	s.c.count(func(st *Stats) { st.RxVoice += uint64(len(frames)) })
	if !s.redundant {
		s.lastVoiceAt = now
		s.setRxKey(true)
	}
}

func (s *session) handleCmd(cmd command) {
	now := s.c.clock.Now()
	var err error
	switch cmd.kind {
	case cmdKey:
		if s.state != stateUp {
			err = fmt.Errorf("iax2: call not up")
			break
		}
		s.setTx(cmd.on, now)
	case cmdText:
		if s.state != stateUp {
			err = fmt.Errorf("iax2: call not up")
			break
		}
		s.sendText(cmd.text)
	case cmdLinkList:
		s.links = cmd.links
		if s.state == stateUp {
			s.nextLinkList = now.Add(linkListShort)
		}
	case cmdHangup:
		if s.state == stateHangingUp {
			break
		}
		s.hangup(now, "Normal clearing")
		s.state = stateHangingUp
		s.pendingErr = nil
		// Hangup returns when the call ends (finish closes done).
		return
	case cmdAbort:
		s.hangup(now, "Dial cancelled")
		s.finish(ErrClosed)
	}
	cmd.reply <- err
}

// hangup sends HANGUP and stops transmitting.
func (s *session) hangup(now time.Time, cause string) {
	s.txOn = false
	var ies IEs
	ies.AddString(IECause, cause)
	ies.AddU8(IECauseCode, 16) // normal clearing
	// Only the HANGUP is worth retransmitting from here on.
	s.pend = nil
	s.sendIAX(CmdHangup, ies)
	s.hangupDeadline = now.Add(hangupLinger)
}

func (s *session) setTx(on bool, now time.Time) {
	if s.txOn == on {
		return
	}
	s.txOn = on
	if on {
		// Start the voice clock on a 20 ms boundary after anything sent.
		ts := (s.nowTS(now) + frameMS - 1) / frameMS * frameMS
		if ts <= s.voiceTS {
			ts = s.voiceTS + frameMS
		}
		s.voiceTS = ts - frameMS
		s.voiceFull = true
		s.nextTxAt = now
		if s.redundant {
			s.sendControl(CtlRadioKey)
			s.nextKeyResend = now.Add(redundantTxTime)
		}
		s.pumpTx(now)
		return
	}
	s.c.mu.Lock()
	s.c.txq = nil
	s.c.mu.Unlock()
	s.sendControl(CtlRadioUnkey)
	if s.redundant {
		s.nextKeyResend = now.Add(redundantTxTime)
	}
}

// pumpTx sends the voice frames due at now, up to three to catch up.
func (s *session) pumpTx(now time.Time) {
	if !s.txOn || s.quelched {
		return
	}
	if now.Sub(s.nextTxAt) > 5*FrameDuration {
		s.nextTxAt = now
	}
	for n := 0; n < 3 && !now.Before(s.nextTxAt); n++ {
		var f Frame
		s.c.mu.Lock()
		if len(s.c.txq) > 0 {
			f = s.c.txq[0]
			s.c.txq = s.c.txq[1:]
		}
		s.c.mu.Unlock()
		s.sendVoice(&f)
		s.nextTxAt = s.nextTxAt.Add(FrameDuration)
	}
}

func (s *session) sendVoice(f *Frame) {
	prev := s.voiceTS
	s.voiceTS += frameMS
	payload := EncodeFrame(make([]byte, 0, FrameSamples), f)
	if s.voiceFull || prev&0xffff0000 != s.voiceTS&0xffff0000 {
		s.voiceFull = false
		s.sendFull(TypeVoice, FormatULaw, s.voiceTS, payload)
	} else {
		m := MiniFrame{SrcCall: s.c.localCall, TS: uint16(s.voiceTS), Data: payload}
		s.write(m.Append(nil))
	}
	s.c.count(func(st *Stats) { st.TxVoice++ })
}

func (s *session) retransmit(p *pending, now time.Time) {
	p.f.Retrans = true
	p.f.ISeq = s.iseq
	if b, err := p.f.Append(nil); err == nil {
		s.write(b)
	}
	p.retries++
	p.rto *= 2
	if p.rto > maxRTO {
		p.rto = maxRTO
	}
	p.nextAt = now.Add(p.rto)
	s.c.count(func(st *Stats) { st.Retransmits++ })
}

func (s *session) tick(now time.Time) {
	if s.ended() {
		return
	}
	for _, p := range s.pend {
		if now.Before(p.nextAt) {
			continue
		}
		if p.retries >= maxRetries {
			if s.state == stateHangingUp {
				s.finish(s.pendingErr)
			} else {
				s.finish(fmt.Errorf("%w: node %s", ErrTimeout, s.c.cfg.RemoteNode))
			}
			return
		}
		s.retransmit(p, now)
	}
	switch s.state {
	case stateHangingUp:
		if len(s.pend) == 0 || !now.Before(s.hangupDeadline) {
			s.finish(s.pendingErr)
		}
		return
	case stateDialing, stateAccepted:
		if !now.Before(s.dialDeadline) {
			s.hangup(now, "Dial timeout")
			s.finish(fmt.Errorf("%w: no answer from node %s", ErrTimeout, s.c.cfg.RemoteNode))
		}
		return
	}

	cfg := &s.c.cfg
	if now.Sub(s.lastRx) > cfg.PeerTimeout {
		s.finish(fmt.Errorf("%w: nothing from node %s for %s", ErrTimeout, cfg.RemoteNode, cfg.PeerTimeout))
		return
	}
	if !now.Before(s.nextPing) {
		s.sendIAX(CmdPing, nil)
		s.nextPing = now.Add(cfg.PingInterval)
	}
	if !now.Before(s.nextLag) {
		s.sendIAX(CmdLagRq, nil)
		s.nextLag = now.Add(cfg.LagInterval)
	}
	if !now.Before(s.nextLinkList) {
		if text, err := FormatLinkList(s.links); err == nil {
			s.sendText(text)
		}
		s.nextLinkList = now.Add(cfg.LinkListTime)
	}
	if s.redundant {
		if !now.Before(s.nextKeyResend) {
			if s.txOn {
				s.sendControl(CtlRadioKey)
			} else {
				s.sendControl(CtlRadioUnkey)
			}
			s.nextKeyResend = now.Add(redundantTxTime)
		}
		if s.rxKeyed && now.Sub(s.lastKeyCtlAt) >= redundantRxLimit {
			s.setRxKey(false)
		}
	} else if s.rxKeyed && now.Sub(s.lastVoiceAt) >= cfg.RxLinger {
		s.setRxKey(false)
	}
	s.pumpTx(now)
	s.playout(now)
}

// playout moves one frame from the jitter buffer to Recv.
func (s *session) playout(now time.Time) {
	f, st := s.jb.Get(now)
	js := s.jb.Stats()
	s.c.count(func(x *Stats) { x.Jitter = js })
	if st == PlayIdle {
		return
	}
	select {
	case s.c.recv <- f:
	default:
		s.c.count(func(x *Stats) { x.RxDropped++ })
	}
}
