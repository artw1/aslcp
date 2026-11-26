package iax2

import (
	"encoding/binary"
	"fmt"
	"time"
)

// Information element types (RFC 5456 section 8.6; CALLTOKEN, CAPABILITY2
// and FORMAT2 from Asterisk's iax2.h).
const (
	IECalledNumber  uint8 = 1
	IECallingNumber uint8 = 2
	IECallingName   uint8 = 4
	IECalledContext uint8 = 5
	IEUsername      uint8 = 6
	IEPassword      uint8 = 7
	IECapability    uint8 = 8
	IEFormat        uint8 = 9
	IELanguage      uint8 = 10
	IEVersion       uint8 = 11
	IEADSICPE       uint8 = 12
	IEDNID          uint8 = 13
	IEAuthMethods   uint8 = 14
	IEChallenge     uint8 = 15
	IEMD5Result     uint8 = 16
	IERSAResult     uint8 = 17
	IEApparentAddr  uint8 = 18
	IECallNo        uint8 = 21
	IECause         uint8 = 22
	IEIAXUnknown    uint8 = 23
	IEDateTime      uint8 = 31
	IECallingPres   uint8 = 38
	IECallingTON    uint8 = 39
	IECallingTNS    uint8 = 40
	IECauseCode     uint8 = 42
	IEEncryption    uint8 = 43
	IECodecPrefs    uint8 = 45
	IECallToken     uint8 = 54
	IECapability2   uint8 = 55
	IEFormat2       uint8 = 56
)

// Authentication method bits for IEAuthMethods.
const (
	AuthPlaintext uint16 = 1 << 0
	AuthMD5       uint16 = 1 << 1
	AuthRSA       uint16 = 1 << 2
)

// ProtocolVersion is the IAX protocol version sent in NEW.
const ProtocolVersion = 2

// IE is one information element.
type IE struct {
	Type uint8
	Data []byte
}

// IEs is an ordered list of information elements.
type IEs []IE

// AddString appends a string element.
func (l *IEs) AddString(t uint8, s string) { *l = append(*l, IE{t, []byte(s)}) }

// AddBytes appends a raw element (an empty one is allowed).
func (l *IEs) AddBytes(t uint8, b []byte) { *l = append(*l, IE{t, append([]byte{}, b...)}) }

// AddU8 appends a one-byte element.
func (l *IEs) AddU8(t uint8, v uint8) { *l = append(*l, IE{t, []byte{v}}) }

// AddU16 appends a two-byte element.
func (l *IEs) AddU16(t uint8, v uint16) {
	*l = append(*l, IE{t, binary.BigEndian.AppendUint16(nil, v)})
}

// AddU32 appends a four-byte element.
func (l *IEs) AddU32(t uint8, v uint32) {
	*l = append(*l, IE{t, binary.BigEndian.AppendUint32(nil, v)})
}

// AddFormat2 appends a CAPABILITY2 or FORMAT2 element: version byte 0 and a
// 64-bit format mask.
func (l *IEs) AddFormat2(t uint8, v uint64) {
	b := []byte{0}
	*l = append(*l, IE{t, binary.BigEndian.AppendUint64(b, v)})
}

// Encode serializes the list. Elements longer than 255 bytes are an error.
func (l IEs) Encode() ([]byte, error) {
	var out []byte
	for _, ie := range l {
		if len(ie.Data) > 255 {
			return nil, fmt.Errorf("iax2: IE %d too long (%d bytes)", ie.Type, len(ie.Data))
		}
		out = append(out, ie.Type, byte(len(ie.Data)))
		out = append(out, ie.Data...)
	}
	return out, nil
}

// ParseIEs parses the payload of an IAX frame. Data slices alias b.
func ParseIEs(b []byte) (IEs, error) {
	var l IEs
	for len(b) > 0 {
		if len(b) < 2 {
			return l, fmt.Errorf("iax2: truncated IE header")
		}
		t, n := b[0], int(b[1])
		if len(b) < 2+n {
			return l, fmt.Errorf("iax2: IE %d length %d exceeds payload", t, n)
		}
		l = append(l, IE{t, b[2 : 2+n]})
		b = b[2+n:]
	}
	return l, nil
}

// Get returns the first element of type t.
func (l IEs) Get(t uint8) ([]byte, bool) {
	for _, ie := range l {
		if ie.Type == t {
			return ie.Data, true
		}
	}
	return nil, false
}

// String returns element t as a string, or "".
func (l IEs) String(t uint8) string {
	b, _ := l.Get(t)
	return string(b)
}

// U16 returns a two-byte element.
func (l IEs) U16(t uint8) (uint16, bool) {
	b, ok := l.Get(t)
	if !ok || len(b) != 2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(b), true
}

// U32 returns a four-byte element.
func (l IEs) U32(t uint8) (uint32, bool) {
	b, ok := l.Get(t)
	if !ok || len(b) != 4 {
		return 0, false
	}
	return binary.BigEndian.Uint32(b), true
}

// Format returns the media format from FORMAT2 if present, else FORMAT.
func (l IEs) Format() (uint64, bool) {
	if b, ok := l.Get(IEFormat2); ok && len(b) == 9 {
		return binary.BigEndian.Uint64(b[1:]), true
	}
	if v, ok := l.U32(IEFormat); ok {
		return uint64(v), true
	}
	return 0, false
}

// PackDateTime encodes t in the IAX DATETIME layout: year since 2000 (7
// bits), month, day, hour, minute and seconds/2.
func PackDateTime(t time.Time) uint32 {
	y := t.Year() - 2000
	if y < 0 {
		y = 0
	}
	return uint32(y&0x7f)<<25 | uint32(t.Month())<<21 | uint32(t.Day())<<16 |
		uint32(t.Hour())<<11 | uint32(t.Minute())<<5 | uint32(t.Second()/2)
}
