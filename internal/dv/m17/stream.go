// SPDX-License-Identifier: GPL-3.0-or-later

package m17

import (
	"bytes"
	"encoding/binary"
	"errors"
)

// Stream packet geometry.
const (
	StreamLen   = 54
	PayloadLen  = 16
	CodecFrame  = 8 // one Codec2 3200 frame, 20 ms
	LastFrame   = 0x8000
	FNMask      = 0x7FFF
	streamCRCAt = 52
)

// StreamMagic opens a stream packet.
var StreamMagic = [4]byte{'M', '1', '7', ' '}

// Stream packet errors. ErrStreamCRC is returned together with the parsed
// packet so a caller that does not require the CRC (many senders write 0)
// can use errors.Is and carry on.
var (
	ErrStreamLength = errors.New("m17: stream packet is not 54 bytes")
	ErrStreamMagic  = errors.New("m17: stream packet magic is not \"M17 \"")
	ErrStreamCRC    = errors.New("m17: stream packet CRC mismatch")
)

// Stream is an M17-over-IP stream packet.
type Stream struct {
	StreamID uint16
	LSF      LSF
	FN       uint16 // bit 15 = last frame; bits 14..0 counter
	Payload  [PayloadLen]byte
}

// Counter returns the frame counter without the last-frame bit.
func (s *Stream) Counter() uint16 { return s.FN & FNMask }

// Last reports the last-frame bit.
func (s *Stream) Last() bool { return s.FN&LastFrame != 0 }

// NextFN returns the frame number after fn: the counter increments and wraps
// from 0x7FFF to 0; the last-frame bit is cleared.
func NextFN(fn uint16) uint16 { return (fn + 1) & FNMask }

// Marshal builds the 54-byte packet with a correct CRC.
func (s *Stream) Marshal() [StreamLen]byte {
	var b [StreamLen]byte
	copy(b[0:4], StreamMagic[:])
	binary.BigEndian.PutUint16(b[4:6], s.StreamID)
	lsd := s.LSF.Marshal()
	copy(b[6:34], lsd[:])
	binary.BigEndian.PutUint16(b[34:36], s.FN)
	copy(b[36:52], s.Payload[:])
	binary.BigEndian.PutUint16(b[streamCRCAt:], CRC16(b[:streamCRCAt]))
	return b
}

// UnmarshalStream parses a stream packet: exactly 54 bytes, magic "M17 ",
// CRC over the first 52 bytes. On ErrStreamCRC the packet is still filled.
func UnmarshalStream(b []byte) (Stream, error) {
	var s Stream
	if len(b) != StreamLen {
		return s, ErrStreamLength
	}
	if !bytes.Equal(b[0:4], StreamMagic[:]) {
		return s, ErrStreamMagic
	}
	s.StreamID = binary.BigEndian.Uint16(b[4:6])
	s.LSF, _ = UnmarshalLSF(b[6:34])
	s.FN = binary.BigEndian.Uint16(b[34:36])
	copy(s.Payload[:], b[36:52])
	if CRC16(b[:streamCRCAt]) != binary.BigEndian.Uint16(b[streamCRCAt:]) {
		return s, ErrStreamCRC
	}
	return s, nil
}
