// SPDX-License-Identifier: GPL-3.0-or-later

package m17

import (
	"encoding/binary"
	"errors"
)

// Sizes of the link setup data.
const (
	LSDLen  = 28 // DST 6, SRC 6, TYPE 2, META 14
	LSFLen  = 30 // LSD + CRC-16
	MetaLen = 14
)

// TYPE field values.
const (
	DataTypeData       = 1
	DataTypeVoice      = 2
	DataTypeVoiceData  = 3
	EncNone            = 0
	typeStream         = 1 << 0
	typeSigned         = 1 << 11
	typeReservedMask   = 0xF000
	typeVoice3200Plain = typeStream | DataTypeVoice<<1 // 0x0005
)

// LSF errors.
var (
	ErrLSFLength = errors.New("m17: bad LSF length")
	ErrLSFCRC    = errors.New("m17: LSF CRC mismatch")
	ErrCAN       = errors.New("m17: CAN out of range 0..15")
)

// Type is the 16-bit TYPE field.
type Type uint16

// VoiceType returns the TYPE of a plain (unencrypted, unsigned) Codec2 3200
// voice stream with the given CAN.
func VoiceType(can uint8) (Type, error) {
	if can > 15 {
		return 0, ErrCAN
	}
	return Type(typeVoice3200Plain | uint16(can)<<7), nil
}

// Stream reports whether the stream (not packet) bit is set.
func (t Type) Stream() bool { return t&typeStream != 0 }

// DataType returns bits 2..1 (1 data, 2 voice, 3 voice+data).
func (t Type) DataType() uint8 { return uint8(t>>1) & 3 }

// EncType returns the encryption type, bits 4..3 (0 none).
func (t Type) EncType() uint8 { return uint8(t>>3) & 3 }

// EncSubtype returns bits 6..5.
func (t Type) EncSubtype() uint8 { return uint8(t>>5) & 3 }

// CAN returns the channel access number, bits 10..7.
func (t Type) CAN() uint8 { return uint8(t>>7) & 15 }

// Signed reports the signed-stream bit.
func (t Type) Signed() bool { return t&typeSigned != 0 }

// Encrypted reports a non-zero encryption type.
func (t Type) Encrypted() bool { return t.EncType() != EncNone }

// Reserved reports whether any reserved bit (15..12) is set.
func (t Type) Reserved() bool { return t&typeReservedMask != 0 }

// IsVoice reports a voice stream (voice or voice+data; Codec2 3200 carries
// both as 3200 in the voice payload layout).
func (t Type) IsVoice() bool { return t.Stream() && t.DataType() == DataTypeVoice }

// LSF is the link setup frame content.
type LSF struct {
	Dst  Address
	Src  Address
	Type Type
	Meta [MetaLen]byte
}

// Marshal appends the 28-byte LSD form (no CRC).
func (l *LSF) Marshal() [LSDLen]byte {
	var b [LSDLen]byte
	d, s := l.Dst.Bytes(), l.Src.Bytes()
	copy(b[0:6], d[:])
	copy(b[6:12], s[:])
	binary.BigEndian.PutUint16(b[12:14], uint16(l.Type))
	copy(b[14:], l.Meta[:])
	return b
}

// MarshalCRC returns the 30-byte over-the-air LSF: LSD plus CRC-16.
func (l *LSF) MarshalCRC() [LSFLen]byte {
	var b [LSFLen]byte
	d := l.Marshal()
	copy(b[:], d[:])
	binary.BigEndian.PutUint16(b[LSDLen:], CRC16(d[:]))
	return b
}

// UnmarshalLSF parses exactly 28 bytes.
func UnmarshalLSF(b []byte) (LSF, error) {
	var l LSF
	if len(b) != LSDLen {
		return l, ErrLSFLength
	}
	l.Dst = AddressFromBytes(b[0:6])
	l.Src = AddressFromBytes(b[6:12])
	l.Type = Type(binary.BigEndian.Uint16(b[12:14]))
	copy(l.Meta[:], b[14:])
	return l, nil
}

// UnmarshalLSFCRC parses exactly 30 bytes and checks the CRC. On a CRC
// mismatch the parsed LSF is returned together with ErrLSFCRC.
func UnmarshalLSFCRC(b []byte) (LSF, error) {
	if len(b) != LSFLen {
		return LSF{}, ErrLSFLength
	}
	l, _ := UnmarshalLSF(b[:LSDLen])
	if CRC16(b[:LSDLen]) != binary.BigEndian.Uint16(b[LSDLen:]) {
		return l, ErrLSFCRC
	}
	return l, nil
}
