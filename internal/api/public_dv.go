package api

import (
	"time"

	"github.com/artw1/aslcp/internal/hamdb"
	"github.com/artw1/aslcp/internal/nowcalling"
	"github.com/artw1/aslcp/internal/public"
)

// publicDV hands the digital modes calls of the Now calling tracker to the
// public service (only read while the admin setting "Show digital modes
// activity" is on). Everything is copied into neutral fields; the public
// package decides what may be published. Names and places come from what the
// tracker already resolved, completed from the local radioid.net list and the
// hamdb.org cache with cache-only reads: a request of a visitor never makes or
// queues a lookup.
func (s *Server) publicDV() public.DVInput {
	if s.nc == nil || s.nc.tr == nil {
		return public.DVInput{}
	}
	sn := s.nc.tr.Snapshot()
	var in public.DVInput
	if c := sn.Current; c != nil && c.Kind != nowcalling.KindAllStar {
		if x, ok := s.publicDVCall(*c); ok {
			in.Now = &x
		}
	}
	for _, c := range sn.Recent {
		if c.Kind == nowcalling.KindAllStar {
			continue
		}
		if x, ok := s.publicDVCall(c); ok {
			in.Heard = append(in.Heard, x)
		}
	}
	return in
}

func (s *Server) publicDVCall(c nowcalling.Call) (public.DVCallIn, bool) {
	x := public.DVCallIn{Call: c.Call, Slot: c.Slot, Private: c.Private, Name: c.Name, Place: joinPlace(c.City, c.State)}
	switch c.Kind {
	case nowcalling.KindDMR:
		switch c.Network {
		case "", "DMR", "BrandMeister":
			x.Mode = "DMR"
		case "Echo test":
			return x, false
		default:
			x.Mode = c.Network // P25, NXDN
		}
		if c.TG != "" {
			x.Target = "TG " + c.TG
		}
	case nowcalling.KindYSF:
		x.Mode, x.Target = "YSF", c.Room
	case nowcalling.KindM17:
		x.Mode, x.Target = "M17", c.Room
	default:
		return x, false
	}
	if t, err := time.Parse(time.RFC3339, c.Start); err == nil {
		x.Start = t
	}
	if x.Name == "" && x.Call != "" && !x.Private {
		x.Name, x.Place = s.cachedPerson(x.Call)
	}
	return x, true
}

// cachedPerson is callerPerson without any chance of a lookup: the radioid.net
// list, then the hamdb.org cache read with Cached (never Lookup, which queues).
func (s *Server) cachedPerson(call string) (name, place string) {
	if s.d.RadioID != nil {
		if u, ok := s.d.RadioID.ByCallsign(call); ok {
			return u.Name(), joinPlace(u.City, u.State)
		}
	}
	if s.d.HamDB != nil && hamdb.ValidCall(call) {
		if in, ok := s.d.HamDB.Cached(call); ok {
			return in.Name, in.Place()
		}
	}
	return "", ""
}
