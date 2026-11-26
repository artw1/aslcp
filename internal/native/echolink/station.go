package echolink

import (
	"fmt"
	"net/netip"
	"strconv"
	"strings"
)

// Kind is the type of an EchoLink station, taken from its callsign.
type Kind int

// Station kinds.
const (
	KindUser       Kind = iota // a single user
	KindLink                   // a simplex link (-L)
	KindRepeater               // a repeater (-R)
	KindConference             // a conference server (*NAME*)
)

// String returns the kind's name.
func (k Kind) String() string {
	switch k {
	case KindLink:
		return "link"
	case KindRepeater:
		return "repeater"
	case KindConference:
		return "conference"
	default:
		return "user"
	}
}

// Station is one entry of the directory's station list.
type Station struct {
	Callsign string     // upper case, e.g. N0CALL-L or *EXAMPLE*
	Location string     // the free-text description without the status
	Status   string     // e.g. "ON" or "BUSY"; empty if not given
	Time     string     // the station's local logon time, "HH:MM"; may be empty
	ID       int        // EchoLink node number
	Addr     netip.Addr // public IPv4 address
}

// Kind classifies the station by its callsign.
func (s Station) Kind() Kind {
	switch {
	case strings.HasPrefix(s.Callsign, "*"):
		return KindConference
	case strings.HasSuffix(s.Callsign, "-L"):
		return KindLink
	case strings.HasSuffix(s.Callsign, "-R"):
		return KindRepeater
	default:
		return KindUser
	}
}

// Busy reports whether the station said it is busy.
func (s Station) Busy() bool { return strings.EqualFold(s.Status, "BUSY") }

// splitDescription separates "Location text [ON 12:34]" into its parts. A
// description without a trailing bracket is all location.
func splitDescription(desc string) (location, status, hhmm string) {
	desc = strings.TrimSpace(desc)
	if !strings.HasSuffix(desc, "]") {
		return desc, "", ""
	}
	i := strings.LastIndexByte(desc, '[')
	if i < 0 {
		return desc, "", ""
	}
	inner := strings.Fields(desc[i+1 : len(desc)-1])
	location = strings.TrimSpace(desc[:i])
	if len(inner) > 0 {
		status = inner[0]
	}
	if len(inner) > 1 {
		hhmm = inner[1]
	}
	return location, status, hhmm
}

// normalizeTarget upper-cases and trims a callsign or node number.
func normalizeTarget(target string) string {
	return strings.ToUpper(strings.TrimSpace(target))
}

// findStation looks target up in list. target is a node number or a callsign;
// a conference matches with or without its surrounding asterisks.
func findStation(list []Station, target string) (Station, error) {
	t := normalizeTarget(target)
	if t == "" {
		return Station{}, fmt.Errorf("%w: empty target", ErrNotFound)
	}
	if id, err := strconv.Atoi(t); err == nil {
		for _, s := range list {
			if s.ID == id {
				return s, nil
			}
		}
		return Station{}, fmt.Errorf("%w: node %d", ErrNotFound, id)
	}
	conf := "*" + strings.Trim(t, "*") + "*"
	for _, s := range list {
		if s.Callsign == t || s.Callsign == conf {
			return s, nil
		}
	}
	return Station{}, fmt.Errorf("%w: %s", ErrNotFound, t)
}
