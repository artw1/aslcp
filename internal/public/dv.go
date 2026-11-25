package public

import (
	"regexp"
	"strings"
	"time"
	"unicode"
)

// Digital modes (DV) data of the public page. It is sent only while the admin
// setting "Show digital modes activity" is on (Settings.ShowDV, off by
// default); with it off none of this is built or sent.
//
// The structs here are an allowlist, like the rest of this package: a call
// carries its mode, callsign, target, time slot, start time and, when already
// known from a local cache (the radioid.net list or the hamdb.org cache), the
// caller's name and place. There is no place for a master or server host, a
// login state, the bridge node, a DMR ID, an identity or settings, an address
// or a version. The input (DVInput) is filled by the server from its Now
// calling tracker; this package never looks anything up itself, so a request
// of a visitor can never cause a lookup.

// DVLimit is the number of DV calls in the heard list.
const DVLimit = 10

// DVCall is one digital modes call. Callsign, Name and Location are omitted
// when callsigns are hidden.
type DVCall struct {
	Mode     string `json:"mode"`
	Callsign string `json:"callsign,omitempty"`
	Target   string `json:"target,omitempty"` // "TG 91", a reflector name
	Slot     int    `json:"slot,omitempty"`
	At       int64  `json:"at"` // start, unix seconds
	Name     string `json:"name,omitempty"`
	Location string `json:"location,omitempty"`
}

// DV is the digital modes part of the page: the call in progress (nil when
// nobody is talking) and the last heard calls, newest first.
type DV struct {
	Now   *DVCall  `json:"now"`
	Heard []DVCall `json:"heard"`
}

// DVCallIn is one call as the server hands it over. It is not sent anywhere;
// BuildDV copies the fields that may be published.
type DVCallIn struct {
	Mode   string // DMR, YSF, P25, NXDN, M17
	Call   string
	Target string
	Slot   int
	Start  time.Time
	// Name and Place are known from a cache only (never looked up for the visitor).
	Name, Place string
	Private     bool
}

// DVInput is what Options.DV returns: the call in progress and the recent calls, newest first.
type DVInput struct {
	Now   *DVCallIn
	Heard []DVCallIn
}

var (
	dvModes  = map[string]bool{"DMR": true, "YSF": true, "P25": true, "NXDN": true, "M17": true}
	reTarget = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 _#-]{0,23}$`) // no dots or colons: never a host name or an address
	reCall   = regexp.MustCompile(`^[A-Za-z0-9/-]{3,12}$`)
)

// dvCallsign accepts what looks like an amateur callsign (letters and a
// digit): a DMR ID, "Echo test" and the like are dropped.
func dvCallsign(s string) string {
	s = strings.TrimSpace(s)
	if !reCall.MatchString(s) || strings.IndexFunc(s, unicode.IsLetter) < 0 || strings.IndexFunc(s, unicode.IsDigit) < 0 {
		return ""
	}
	return strings.ToUpper(s)
}

func dvText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune("<>\"&", r) {
			return -1
		}
		return r
	}, scrub(strings.TrimSpace(s)))
	if r := []rune(s); len(r) > max {
		s = string(r[:max])
	}
	return strings.TrimSpace(s)
}

func buildDVCall(in DVCallIn, v View) (DVCall, bool) {
	if in.Private || !dvModes[in.Mode] {
		return DVCall{}, false
	}
	c := DVCall{Mode: in.Mode, At: in.Start.Unix()}
	if reTarget.MatchString(strings.TrimSpace(in.Target)) {
		c.Target = strings.TrimSpace(in.Target)
	}
	if in.Slot == 1 || in.Slot == 2 {
		c.Slot = in.Slot
	}
	if v.Callsigns {
		c.Callsign = dvCallsign(in.Call)
		if c.Callsign != "" {
			c.Name, c.Location = dvText(in.Name, 40), dvText(in.Place, 40)
		}
	}
	return c, true
}

// BuildDV converts the server's DV input to the public form. Private calls
// and calls of an unknown mode are left out; with callsigns hidden the
// callsign, name and place are left out.
func BuildDV(in DVInput, v View) *DV {
	out := &DV{Heard: []DVCall{}}
	if in.Now != nil {
		if c, ok := buildDVCall(*in.Now, v); ok {
			out.Now = &c
		}
	}
	for _, h := range in.Heard {
		if len(out.Heard) >= DVLimit {
			break
		}
		if c, ok := buildDVCall(h, v); ok {
			out.Heard = append(out.Heard, c)
		}
	}
	return out
}

func (s *Service) dv(cfg *Settings, v View) *DV {
	if !cfg.ShowDV {
		return nil
	}
	if s.opt.DV == nil {
		return &DV{Heard: []DVCall{}}
	}
	return BuildDV(s.opt.DV(), v)
}

// DVInterval is the shortest time between two "dv" events on the stream.
const dvInterval = time.Second

// DVChanged tells the service that the digital modes calls changed. Updates
// are coalesced: at most one "dv" event per DVInterval goes to the clients,
// and nothing at all while the setting is off or nobody is watching.
func (s *Service) DVChanged() {
	cfg := s.cfg.Load()
	if !cfg.Enabled || !cfg.ShowDV || s.hub.count() == 0 {
		return
	}
	s.dvMu.Lock()
	if s.dvTimer != nil { // one is scheduled already and will read the newest data
		s.dvMu.Unlock()
		return
	}
	now := s.opt.Now()
	wait := s.DVInterval - now.Sub(s.dvLast)
	if wait <= 0 {
		s.dvLast = now
		s.dvMu.Unlock()
		s.sendDV()
		return
	}
	s.dvTimer = time.AfterFunc(wait, func() {
		s.dvMu.Lock()
		s.dvTimer = nil
		s.dvLast = s.opt.Now()
		s.dvMu.Unlock()
		s.sendDV()
	})
	s.dvMu.Unlock()
}

func (s *Service) sendDV() {
	cfg := s.cfg.Load()
	if !cfg.Enabled || !cfg.ShowDV || s.hub.count() == 0 {
		return
	}
	d := s.dv(cfg, s.view(cfg, s.opt.Now()))
	if d == nil {
		return
	}
	f := frame("dv", d)
	// The tracker also reports AllStar changes: send only what differs from the last event.
	s.dvMu.Lock()
	same := string(f) == string(s.dvSent)
	s.dvSent = f
	s.dvMu.Unlock()
	if !same {
		s.hub.broadcast(f)
	}
}

func (s *Service) stopDV() {
	s.dvMu.Lock()
	if s.dvTimer != nil {
		s.dvTimer.Stop()
		s.dvTimer = nil
	}
	s.dvMu.Unlock()
}
