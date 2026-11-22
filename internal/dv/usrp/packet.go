// Package usrp implements the USRP UDP protocol spoken by Asterisk's
// chan_usrp: a 32-byte header optionally followed by a payload (160 signed
// 16-bit little-endian PCM samples for voice, one digit byte for DTMF, a TLV
// blob for text).
package usrp

import (
	"encoding/binary"
	"errors"
	"fmt"
)

const (
	// HeaderLen is the fixed header size.
	HeaderLen = 32
	// Samples is the PCM samples per voice frame (20 ms at 8 kHz).
	Samples = 160
	// VoiceLen is the voice payload size in bytes.
	VoiceLen = Samples * 2
	// MaxText bounds a TEXT payload.
	MaxText = 64 + 2
	// MaxPacket is the largest datagram Parse accepts.
	MaxPacket = HeaderLen + VoiceLen
)

// Type is the packet type field.
type Type uint32

const (
	TypeVoice Type = 0
	TypeDTMF  Type = 1
	TypeText  Type = 2
)

// TLVSetInfo is the TLV tag of the metadata ("set info") record.
const TLVSetInfo = 0x08

var eye = [4]byte{'U', 'S', 'R', 'P'}

// Errors returned by Parse.
var (
	ErrShort   = errors.New("usrp: short packet")
	ErrEye     = errors.New("usrp: bad eye catcher")
	ErrPayload = errors.New("usrp: bad payload length")
	ErrType    = errors.New("usrp: unknown type")
)

// Header fields: eye "USRP" [0:4], Seq BE [4:8], Memory [8:12], Keyup BE
// [12:16], Talkgroup [16:20], Type BE [20:24], MpxID [24:28], Reserved
// [28:32].
type Packet struct {
	Seq       uint32
	Memory    uint32
	Keyup     bool
	Talkgroup uint32
	Type      Type
	PCM       *[Samples]int16 // VOICE with payload; nil for a bare header
	DTMF      byte
	Text      []byte // TEXT: raw TLV
}

// Parse decodes one datagram. A voice packet carries either no payload (a
// bare header, as sent at the end of a transmission) or exactly 320 bytes.
func Parse(b []byte) (Packet, error) {
	var p Packet
	if len(b) < HeaderLen {
		return p, ErrShort
	}
	if [4]byte(b[0:4]) != eye {
		return p, ErrEye
	}
	p.Seq = binary.BigEndian.Uint32(b[4:8])
	p.Memory = binary.BigEndian.Uint32(b[8:12])
	p.Keyup = binary.BigEndian.Uint32(b[12:16]) != 0
	p.Talkgroup = binary.BigEndian.Uint32(b[16:20])
	p.Type = Type(binary.BigEndian.Uint32(b[20:24]))
	body := b[HeaderLen:]
	switch p.Type {
	case TypeVoice:
		switch len(body) {
		case 0:
		case VoiceLen:
			p.PCM = new([Samples]int16)
			for i := range p.PCM {
				p.PCM[i] = int16(binary.LittleEndian.Uint16(body[2*i:]))
			}
		default:
			return p, fmt.Errorf("%w: voice %d", ErrPayload, len(body))
		}
	case TypeDTMF:
		if len(body) > 1 {
			return p, fmt.Errorf("%w: dtmf %d", ErrPayload, len(body))
		}
		if len(body) == 1 {
			p.DTMF = body[0]
		}
	case TypeText:
		if len(body) > 255+2 {
			return p, fmt.Errorf("%w: text %d", ErrPayload, len(body))
		}
		p.Text = append([]byte(nil), body...)
	default:
		return p, ErrType
	}
	return p, nil
}

// Append encodes p onto dst.
func (p *Packet) Append(dst []byte) []byte {
	var h [HeaderLen]byte
	copy(h[0:4], eye[:])
	binary.BigEndian.PutUint32(h[4:], p.Seq)
	binary.BigEndian.PutUint32(h[8:], p.Memory)
	if p.Keyup {
		binary.BigEndian.PutUint32(h[12:], 1)
	}
	binary.BigEndian.PutUint32(h[16:], p.Talkgroup)
	binary.BigEndian.PutUint32(h[20:], uint32(p.Type))
	dst = append(dst, h[:]...)
	switch p.Type {
	case TypeVoice:
		if p.PCM != nil {
			for _, s := range p.PCM {
				dst = binary.LittleEndian.AppendUint16(dst, uint16(s))
			}
		}
	case TypeDTMF:
		dst = append(dst, p.DTMF)
	case TypeText:
		dst = append(dst, p.Text...)
	}
	return dst
}

// MetaTLV builds a TLV record (tag 0x08, length, ASCII) from s, truncated to
// 64 bytes. Non-printable bytes are replaced by '?'.
func MetaTLV(s string) []byte {
	if len(s) > 64 {
		s = s[:64]
	}
	out := make([]byte, 0, 2+len(s))
	out = append(out, TLVSetInfo, byte(len(s)))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c > 0x7e {
			c = '?'
		}
		out = append(out, c)
	}
	return out
}

// ParseTLV returns the value of the first record with the given tag.
func ParseTLV(b []byte, tag byte) ([]byte, bool) {
	for len(b) >= 2 {
		n := int(b[1])
		if len(b) < 2+n {
			return nil, false
		}
		if b[0] == tag {
			return b[2 : 2+n], true
		}
		b = b[2+n:]
	}
	return nil, false
}
