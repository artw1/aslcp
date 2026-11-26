package node

import (
	"context"
	"time"

	"github.com/artw1/aslcp/internal/native/audio"
	"github.com/artw1/aslcp/internal/native/mode"
)

// stepParams are the station settings the 20 ms step uses, derived from
// the config whenever it changes.
type stepParams struct {
	hang      time.Duration
	txTimeout time.Duration
	courtesy  bool
	agc       audio.AGCParams
	modeAGC   bool
	radioAGC  bool
	id        idParams
	callsign  string
	cw        audio.CWOptions
	voice     string
}

func paramsFor(c *Config) stepParams {
	st := c.Station
	p := stepParams{
		hang:      st.Advanced.Hang(),
		txTimeout: st.Advanced.TxTimeout(),
		courtesy:  st.Advanced.Courtesy(),
		callsign:  st.Callsign,
		cw:        st.ID.CW(),
		voice:     st.ID.VoiceFile,
		id: idParams{
			on:       st.ID.IDOn() && st.Callsign != "",
			interval: st.ID.Interval(),
			tail:     st.ID.TailWindow(),
			endQuiet: st.ID.EndQuiet(),
		},
	}
	p.agc, p.modeAGC, p.radioAGC = st.Advanced.AGC()
	return p
}

// simplex is the radio-side state the step keeps. Guarded by Engine.mu.
type simplex struct {
	ptt       bool
	hangUntil time.Time

	cos      bool      // last carrier state seen
	rfStart  time.Time // a local transmission into the receiver began
	rfSentTo mode.Mode // the mode that received its audio
	rfLocked bool      // timed out: ignore the carrier until it drops

	netStart time.Time // the network started keying the transmitter
	txLocked bool      // timed out: ignore the network until it stops

	tone audio.Source // courtesy tone playing
	id   audio.Source // ID playing
	idT  idTimer

	modeAGC, radioAGC *audio.AGC
}

// maxCatchUp bounds how many captured frames one step forwards when the
// sound card clock runs ahead of the step clock.
const maxCatchUp = 3

// Run steps the engine on clock until ctx ends. It drops PTT before it
// returns.
func (e *Engine) Run(ctx context.Context, clock audio.Clock) {
	defer e.dropPTT()
	for {
		select {
		case <-ctx.Done():
			return
		case t := <-clock.C():
			e.Step(t)
		}
	}
}

// Step runs one 20 ms frame of the simplex logic at now. It never blocks.
func (e *Engine) Step(now time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := &e.sx
	p := e.params
	var m mode.Mode
	if e.active != nil {
		m = e.active.m
	}

	// Radio side: a local station transmitting into our receiver. While
	// we transmit the receiver hears nothing of use (simplex).
	carrier := e.o.Radio.Carrier()
	if carrier != s.cos {
		s.cos = carrier
		e.publishOn(EventCOS, carrier, now)
	}
	rfActive := carrier && !s.ptt
	if !carrier {
		s.rfLocked = false
	}
	e.stepRF(now, m, rfActive)

	// Network side: the active mode's audio for the radio.
	var nf audio.Frame
	haveNet, netTalking := false, false
	if m != nil {
		if rfActive {
			drain(m.Recv()) // the receiver has priority
		} else {
			select {
			case nf = <-m.Recv():
				haveNet = true
			default:
			}
		}
		netTalking = haveNet || m.Talking()
	}
	if !netTalking {
		s.netStart, s.txLocked = time.Time{}, false
		s.modeAGC.Reset()
	}
	wantNet := netTalking && !rfActive && !s.txLocked
	if wantNet {
		if s.netStart.IsZero() {
			s.netStart = now
		}
		if now.Sub(s.netStart) >= p.txTimeout {
			s.txLocked, wantNet = true, false
			e.logf(now, "transmit timeout: the network has keyed the radio for %s", p.txTimeout)
		}
	}

	// Mix what goes on the air.
	var out audio.Frame
	if wantNet && haveNet {
		out = nf
		if p.modeAGC {
			s.modeAGC.Process(&out, p.agc)
		}
	}
	if rfActive {
		s.tone = nil
	}
	tonePlaying := false
	if s.tone != nil {
		var tf audio.Frame
		if s.tone.Next(&tf) {
			mix(&out, &tf)
			tonePlaying = true
		} else {
			s.tone = nil
		}
	}
	if wantNet || tonePlaying {
		s.idT.transmitted(now, p.id)
	}

	// The station ID: RF only.
	if s.id == nil {
		inTail := s.ptt && !wantNet && !tonePlaying
		if why := s.idT.due(now, p.id, s.ptt, inTail, rfActive); why != idNone {
			if src := e.idSource(now); src != nil {
				if !s.ptt {
					src = &silenceThen{n: leadIn, s: src}
				}
				s.id = src
				s.idT.sent(now, why)
				e.hub.publish(Event{Kind: EventID, At: now, Text: string(why)})
			}
		}
	}
	idPlaying := false
	if s.id != nil {
		var f audio.Frame
		if s.id.Next(&f) {
			mix(&out, &f)
			idPlaying = true
		} else {
			s.id = nil
		}
	}

	// PTT.
	content := wantNet || tonePlaying || idPlaying
	if content {
		s.hangUntil = now.Add(p.hang)
	}
	want := !rfActive && (content || (s.ptt && now.Before(s.hangUntil)))
	if want != s.ptt {
		e.setPTT(want, now)
	}
	if s.ptt {
		e.o.Radio.Play(out)
	}
}

// stepRF forwards the receiver's audio to the active mode while a local
// station transmits, and ends the transmission when it unkeys.
func (e *Engine) stepRF(now time.Time, m mode.Mode, rfActive bool) {
	s := &e.sx
	p := e.params
	capt := e.o.Radio.Capture()
	if !rfActive || s.rfLocked {
		drain(capt)
		if !rfActive && !s.rfStart.IsZero() {
			e.endRF(now, m)
		}
		return
	}
	if s.rfStart.IsZero() {
		s.rfStart = now
		s.radioAGC.Reset()
	}
	if now.Sub(s.rfStart) >= p.txTimeout {
		s.rfLocked = true
		e.logf(now, "receive timeout: the local station has transmitted for %s", p.txTimeout)
		e.endRF(now, m)
		s.rfStart = now // keep the carrier marked busy until it drops
		drain(capt)
		return
	}
	if s.rfSentTo != nil && s.rfSentTo != m {
		s.rfSentTo.EndTx()
		s.rfSentTo = nil
	}
	for i := 0; i < maxCatchUp; i++ {
		var f audio.Frame
		select {
		case f = <-capt:
		default:
			return
		}
		if m == nil {
			continue
		}
		if p.radioAGC {
			s.radioAGC.Process(&f, p.agc)
		}
		m.Send(f)
		s.rfSentTo = m
	}
}

// endRF finishes a local transmission: the mode that got its audio hears
// the end, and the courtesy tone follows on the radio.
func (e *Engine) endRF(now time.Time, m mode.Mode) {
	s := &e.sx
	sent := s.rfSentTo
	locked := s.rfLocked
	s.rfStart, s.rfSentTo = time.Time{}, nil
	if sent == nil {
		return
	}
	sent.EndTx()
	if e.params.courtesy && sent == m && !locked {
		s.tone = audio.CourtesyTone()
	}
}

// idSource builds the ID audio, or nil with a logged reason.
func (e *Engine) idSource(now time.Time) audio.Source {
	p := e.params
	if p.voice != "" {
		w, err := audio.LoadWAV(p.voice)
		if err == nil {
			return w
		}
		e.logf(now, "ID voice file: %v; sending CW", err)
	}
	cw, err := audio.NewCW(p.callsign, p.cw)
	if err != nil {
		e.logf(now, "ID: %v", err)
		return nil
	}
	return cw
}

func (e *Engine) setPTT(on bool, now time.Time) {
	if err := e.o.Radio.SetPTT(on); err != nil {
		e.logf(now, "PTT: %v", err)
		if on {
			return
		}
	}
	e.sx.ptt = on
	if !on {
		e.sx.netStart = time.Time{}
	}
	e.publishOn(EventPTT, on, now)
}

// dropPTT unkeys the transmitter, whatever the state.
func (e *Engine) dropPTT() {
	e.mu.Lock()
	defer e.mu.Unlock()
	_ = e.o.Radio.SetPTT(false)
	if e.sx.ptt {
		e.sx.ptt = false
		e.publishOn(EventPTT, false, e.now())
	}
	e.sx.tone, e.sx.id = nil, nil
}

func (e *Engine) publishOn(k EventKind, on bool, now time.Time) {
	v := on
	e.hub.publish(Event{Kind: k, At: now, On: &v})
}

func (e *Engine) logf(now time.Time, format string, args ...any) {
	ev := Event{Kind: EventLog, At: now, Text: sprintf(format, args...)}
	e.log.Warn(ev.Text)
	e.hub.publish(ev)
}

func drain(c <-chan audio.Frame) {
	for {
		select {
		case <-c:
		default:
			return
		}
	}
}

// mix adds b into a with saturation.
func mix(a, b *audio.Frame) {
	for i := range a {
		v := int32(a[i]) + int32(b[i])
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		a[i] = int16(v)
	}
}
