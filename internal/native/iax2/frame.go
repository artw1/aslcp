package iax2

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
)

// FrameType is the IAX2 full frame type (RFC 5456 section 8.2).
type FrameType uint8

// Frame types used by this client.
const (
	TypeDTMFEnd FrameType = 0x01
	TypeVoice   FrameType = 0x02
	TypeVideo   FrameType = 0x03
	TypeControl FrameType = 0x04
	TypeNull    FrameType = 0x05
	TypeIAX     FrameType = 0x06
	TypeText    FrameType = 0x07
	TypeImage   FrameType = 0x08
	TypeHTML    FrameType = 0x09
	TypeCNG     FrameType = 0x0a
	TypeDTMFBeg FrameType = 0x0c
)

// IAX frame subclasses (RFC 5456 section 8.4; CALLTOKEN from Asterisk).
const (
	CmdNew       uint32 = 1
	CmdPing      uint32 = 2
	CmdPong      uint32 = 3
	CmdAck       uint32 = 4
	CmdHangup    uint32 = 5
	CmdReject    uint32 = 6
	CmdAccept    uint32 = 7
	CmdAuthReq   uint32 = 8
	CmdAuthRep   uint32 = 9
	CmdInval     uint32 = 10
	CmdLagRq     uint32 = 11
	CmdLagRp     uint32 = 12
	CmdVNAK      uint32 = 18
	CmdTxCnt     uint32 = 23
	CmdTxAcc     uint32 = 24
	CmdQuelch    uint32 = 28
	CmdUnquelch  uint32 = 29
	CmdPoke      uint32 = 30
	CmdUnsupport uint32 = 33
	CmdCallToken uint32 = 40
)

// Control frame subclasses (Asterisk AST_CONTROL_* values, which chan_iax2
// carries unchanged).
const (
	CtlHangup      uint32 = 1
	CtlRing        uint32 = 2
	CtlRinging     uint32 = 3
	CtlAnswer      uint32 = 4
	CtlBusy        uint32 = 5
	CtlCongestion  uint32 = 8
	CtlRadioKey    uint32 = 12
	CtlRadioUnkey  uint32 = 13
	CtlProgress    uint32 = 14
	CtlProceeding  uint32 = 15
	CtlSrcUpdate   uint32 = 20
	CtlSrcChange   uint32 = 26
	CtlConnectLine uint32 = 22
)

// Media format bits (the 32-bit FORMAT and CAPABILITY values).
const (
	FormatGSM  uint32 = 1 << 1
	FormatULaw uint32 = 1 << 2
	FormatALaw uint32 = 1 << 3
)

const (
	fullHeaderLen = 12
	miniHeaderLen = 4
	flagFull      = 0x8000
	flagRetrans   = 0x8000
	callNoMask    = 0x7fff
	subclassLog   = 0x80
)

// ErrShort means a datagram is too short for its header.
var ErrShort = errors.New("iax2: short packet")

// ErrMeta means a meta frame (trunk or video), which this client ignores.
var ErrMeta = errors.New("iax2: meta frame not supported")

// FullFrame is an IAX2 full frame. Subclass holds the decoded value: the
// compressed power-of-two form (C bit) is expanded on decode and chosen on
// encode when the value does not fit in 7 bits.
type FullFrame struct {
	SrcCall  uint16 // 15 bits
	DstCall  uint16 // 15 bits
	Retrans  bool   // R bit: this is a retransmission
	TS       uint32 // milliseconds since call start
	OSeq     uint8
	ISeq     uint8
	Type     FrameType
	Subclass uint32
	Data     []byte
}

// MiniFrame is an IAX2 mini frame: voice in the negotiated format with the
// low 16 bits of the timestamp.
type MiniFrame struct {
	SrcCall uint16
	TS      uint16
	Data    []byte
}

// encodeSubclass returns the subclass byte for v.
func encodeSubclass(v uint32) (byte, error) {
	if v < subclassLog {
		return byte(v), nil
	}
	if bits.OnesCount32(v) != 1 {
		return 0, fmt.Errorf("iax2: subclass %d not encodable", v)
	}
	return subclassLog | byte(bits.TrailingZeros32(v)), nil
}

func decodeSubclass(b byte) (uint32, error) {
	if b&subclassLog == 0 {
		return uint32(b), nil
	}
	shift := b &^ subclassLog
	if shift > 31 {
		return 0, fmt.Errorf("iax2: subclass shift %d out of range", shift)
	}
	return 1 << shift, nil
}

// Append encodes f onto dst.
func (f *FullFrame) Append(dst []byte) ([]byte, error) {
	sc, err := encodeSubclass(f.Subclass)
	if err != nil {
		return dst, err
	}
	dcall := f.DstCall & callNoMask
	if f.Retrans {
		dcall |= flagRetrans
	}
	dst = binary.BigEndian.AppendUint16(dst, flagFull|(f.SrcCall&callNoMask))
	dst = binary.BigEndian.AppendUint16(dst, dcall)
	dst = binary.BigEndian.AppendUint32(dst, f.TS)
	dst = append(dst, f.OSeq, f.ISeq, byte(f.Type), sc)
	return append(dst, f.Data...), nil
}

// Append encodes m onto dst.
func (m *MiniFrame) Append(dst []byte) []byte {
	dst = binary.BigEndian.AppendUint16(dst, m.SrcCall&callNoMask)
	dst = binary.BigEndian.AppendUint16(dst, m.TS)
	return append(dst, m.Data...)
}

// Decode parses one datagram. Exactly one of the returned frames is non-nil
// on success. Data slices alias b.
func Decode(b []byte) (*FullFrame, *MiniFrame, error) {
	if len(b) < miniHeaderLen {
		return nil, nil, ErrShort
	}
	w0 := binary.BigEndian.Uint16(b)
	if w0&flagFull == 0 {
		if w0 == 0 {
			return nil, nil, ErrMeta
		}
		return nil, &MiniFrame{
			SrcCall: w0 & callNoMask,
			TS:      binary.BigEndian.Uint16(b[2:]),
			Data:    b[miniHeaderLen:],
		}, nil
	}
	if len(b) < fullHeaderLen {
		return nil, nil, ErrShort
	}
	sc, err := decodeSubclass(b[11])
	if err != nil {
		return nil, nil, err
	}
	w1 := binary.BigEndian.Uint16(b[2:])
	return &FullFrame{
		SrcCall:  w0 & callNoMask,
		DstCall:  w1 & callNoMask,
		Retrans:  w1&flagRetrans != 0,
		TS:       binary.BigEndian.Uint32(b[4:]),
		OSeq:     b[8],
		ISeq:     b[9],
		Type:     FrameType(b[10]),
		Subclass: sc,
		Data:     b[fullHeaderLen:],
	}, nil, nil
}

// countsForSeq reports whether a full frame advances the sequence numbers.
// ACK, INVAL, VNAK, TXCNT and TXACC do not (RFC 5456 section 8.1.2).
func countsForSeq(t FrameType, sc uint32) bool {
	if t != TypeIAX {
		return true
	}
	switch sc {
	case CmdAck, CmdInval, CmdVNAK, CmdTxCnt, CmdTxAcc:
		return false
	}
	return true
}

// String names the frame for logs and test failures.
func (f *FullFrame) String() string {
	return fmt.Sprintf("full{src=%d dst=%d r=%t ts=%d o=%d i=%d type=%d sc=%d len=%d}",
		f.SrcCall, f.DstCall, f.Retrans, f.TS, f.OSeq, f.ISeq, f.Type, f.Subclass, len(f.Data))
}
