// SPDX-License-Identifier: GPL-3.0-or-later

package m17

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Kind is a reflector control packet type.
type Kind string

// Control packet kinds; the string is the 4-byte ASCII magic.
const (
	KindCONN Kind = "CONN"
	KindLSTN Kind = "LSTN"
	KindACKN Kind = "ACKN"
	KindNACK Kind = "NACK"
	KindPING Kind = "PING"
	KindPONG Kind = "PONG"
	KindDISC Kind = "DISC"
)

// Control packet lengths.
const (
	ConnLen  = 11 // CONN, LSTN
	AddrLen  = 10 // PING, PONG, DISC with callsign
	ShortLen = 4  // ACKN, NACK, DISC acknowledgement
)

// Control errors.
var (
	ErrControlLength = errors.New("m17: bad control packet length")
	ErrControlMagic  = errors.New("m17: unknown control packet magic")
	ErrModule        = errors.New("m17: module must be A-Z")
	ErrControlKind   = errors.New("m17: invalid control packet fields for kind")
)

// Control is a decoded reflector control packet.
type Control struct {
	Kind   Kind
	Addr   Address // CONN, LSTN, PING, PONG, DISC (long form)
	Module byte    // CONN, LSTN only
	Short  bool    // 4-byte form: ACKN, NACK, DISC acknowledgement
}

// ValidModule reports whether m is 'A'..'Z'.
func ValidModule(m byte) bool { return m >= 'A' && m <= 'Z' }

// Constructors for the packets a client sends.

// Conn is the link request to module of a reflector.
func Conn(callsign Address, module byte) Control {
	return Control{Kind: KindCONN, Addr: callsign, Module: module}
}

// Lstn is the listen-only link request.
func Lstn(callsign Address, module byte) Control {
	return Control{Kind: KindLSTN, Addr: callsign, Module: module}
}

// Pong is the keepalive reply.
func Pong(callsign Address) Control { return Control{Kind: KindPONG, Addr: callsign} }

// Disc is the client disconnect.
func Disc(callsign Address) Control { return Control{Kind: KindDISC, Addr: callsign} }

// Marshal encodes the packet, validating the fields for its kind.
func (c Control) Marshal() ([]byte, error) {
	switch c.Kind {
	case KindCONN, KindLSTN:
		if !ValidModule(c.Module) {
			return nil, ErrModule
		}
		if c.Short {
			return nil, ErrControlKind
		}
		b := make([]byte, ConnLen)
		copy(b, c.Kind)
		a := c.Addr.Bytes()
		copy(b[4:10], a[:])
		b[10] = c.Module
		return b, nil
	case KindPING, KindPONG:
		if c.Short || c.Module != 0 {
			return nil, ErrControlKind
		}
		return c.marshalAddr(), nil
	case KindDISC:
		if c.Module != 0 {
			return nil, ErrControlKind
		}
		if c.Short {
			return []byte(c.Kind), nil
		}
		return c.marshalAddr(), nil
	case KindACKN, KindNACK:
		if !c.Short || c.Module != 0 || c.Addr != 0 {
			return nil, ErrControlKind
		}
		return []byte(c.Kind), nil
	}
	return nil, ErrControlMagic
}

func (c Control) marshalAddr() []byte {
	b := make([]byte, AddrLen)
	copy(b, c.Kind)
	a := c.Addr.Bytes()
	copy(b[4:], a[:])
	return b
}

// UnmarshalControl parses a control packet. Accepted forms: CONN/LSTN 11
// bytes with module A-Z; PING/PONG 10 bytes; DISC 10 or 4 bytes; ACKN and
// NACK 4 bytes. Reflector-to-reflector forms (37-byte CONN/ACKN, 10-byte
// NACK) are rejected as ErrControlLength.
func UnmarshalControl(b []byte) (Control, error) {
	if len(b) < 4 {
		return Control{}, ErrControlLength
	}
	k := Kind(b[:4])
	c := Control{Kind: k}
	switch k {
	case KindCONN, KindLSTN:
		if len(b) != ConnLen {
			return Control{}, ErrControlLength
		}
		if !ValidModule(b[10]) {
			return Control{}, ErrModule
		}
		c.Addr = AddressFromBytes(b[4:10])
		c.Module = b[10]
	case KindPING, KindPONG:
		if len(b) != AddrLen {
			return Control{}, ErrControlLength
		}
		c.Addr = AddressFromBytes(b[4:10])
	case KindDISC:
		switch len(b) {
		case AddrLen:
			c.Addr = AddressFromBytes(b[4:10])
		case ShortLen:
			c.Short = true
		default:
			return Control{}, ErrControlLength
		}
	case KindACKN, KindNACK:
		if len(b) != ShortLen {
			return Control{}, ErrControlLength
		}
		c.Short = true
	default:
		return Control{}, ErrControlMagic
	}
	return c, nil
}

// IsStream reports whether b looks like a stream packet by its magic only
// (callers use it to route datagrams before choosing a parser).
func IsStream(b []byte) bool { return bytes.HasPrefix(b, StreamMagic[:]) }

// Target errors.
var ErrTarget = errors.New("m17: target must be \"M17-XXX C\" (reflector, space, module A-Z)")

var reTarget = regexp.MustCompile(`^(M17-[A-Z0-9]{3}) ([A-Z])$`)

// ParseTarget splits a tune target "M17-XXX C" into the reflector
// designator and the module. The input must be exact: upper case, a single
// space, no padding. "disconnect" is not a target; callers test for it
// before calling.
func ParseTarget(s string) (reflector string, module byte, err error) {
	m := reTarget.FindStringSubmatch(s)
	if m == nil {
		return "", 0, fmt.Errorf("%w: %q", ErrTarget, s)
	}
	return m[1], m[2][0], nil
}

// FormatTarget is the inverse of ParseTarget; it is also the callsign form
// used as DST on reflector links.
func FormatTarget(reflector string, module byte) (string, error) {
	s := reflector + " " + string(module)
	if _, _, err := ParseTarget(s); err != nil {
		return "", err
	}
	return s, nil
}

// NodeCallsign builds the callsign a node uses on a reflector link (CONN,
// PONG, DISC and the SRC of its streams): the base callsign upper-cased,
// then the optional suffix, separated by a space unless the suffix starts
// with '-', '/' or '.'. The result must be encodable (at most 9 characters).
func NodeCallsign(base, suffix string) (string, error) {
	base = strings.ToUpper(strings.TrimSpace(base))
	suffix = strings.ToUpper(strings.TrimSpace(suffix))
	if base == "" {
		return "", ErrEmptyCallsign
	}
	s := base
	if suffix != "" {
		if strings.ContainsRune("-/.", rune(suffix[0])) {
			s += suffix
		} else {
			s += " " + suffix
		}
	}
	if _, err := EncodeCallsign(s); err != nil {
		return "", err
	}
	return s, nil
}
