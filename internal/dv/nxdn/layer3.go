package nxdn

// Layer 3 message types (NXDN TS 1-A, MMDVMHost NXDNDefines.h).
const (
	MsgVCall  = 0x01
	MsgTxRel  = 0x08
	MsgIdle   = 0x10
	MsgDCallH = 0x09
	MsgDCallD = 0x0B
)

// Call types stored in the top 3 bits of Layer 3 byte 2.
const (
	CallGroup      = 1 << 5 // conference/group call
	CallIndividual = 4 << 5
)

// Layer3 is the first 80 bits of a Layer 3 message, the part a FACCH1 carries
// (the SACCH superframe carries its first 72 bits). Layout used by MMDVMHost
// NXDNLayer3.cpp:
//
//	0    message type (low 6 bits)
//	1    cause/CC option
//	2    call type (bits 7..5) | voice call option (bits 4..0)
//	3..4 source unit ID
//	5..6 destination group or unit ID
//	7..9 cipher type/key, unused here (zero)
type Layer3 [10]byte

// NewVCall builds the VCALL message of a half duplex EHR voice call.
func NewVCall(src, dst uint16, group bool) Layer3 {
	var l Layer3
	l[0] = MsgVCall
	if group {
		l[2] = CallGroup
	} else {
		l[2] = CallIndividual
	}
	l[3], l[4] = byte(src>>8), byte(src)
	l[5], l[6] = byte(dst>>8), byte(dst)
	return l
}

// TxRel returns the TX_REL that ends the call l (same call, new type, as
// MMDVMHost NXDNControl::writeEndRF).
func (l Layer3) TxRel() Layer3 {
	l[0] = MsgTxRel
	return l
}

func (l Layer3) Type() uint8 { return l[0] & 0x3F }
func (l Layer3) Src() uint16 { return uint16(l[3])<<8 | uint16(l[4]) }
func (l Layer3) Dst() uint16 { return uint16(l[5])<<8 | uint16(l[6]) }

// IsGroup is true unless the call type is individual (MMDVMHost getIsGroup).
func (l Layer3) IsGroup() bool { return l[2]&0x80 != 0x80 }

// SACCHPart returns bits 18*i..18*i+17 (i = 0..3) of the message as the data
// field of the i-th superframe SACCH.
func (l Layer3) SACCHPart(i int) (d [3]byte) {
	for k := 0; k < 18; k++ {
		setBit(d[:], k, getBit(l[:], 18*i+k))
	}
	return d
}

// SetSACCHPart stores a SACCH data field as part i of the message.
func (l *Layer3) SetSACCHPart(i int, d [3]byte) {
	for k := 0; k < 18; k++ {
		setBit(l[:], 18*i+k, getBit(d[:], k))
	}
}
