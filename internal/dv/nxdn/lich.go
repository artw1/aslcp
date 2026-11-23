package nxdn

// LICH RF channel type, functional channel type (USC), option (steal) and
// direction values.
const (
	RFCTRCCH  = 0
	RFCTRTCH  = 1
	RFCTRDCH  = 2
	RFCTRTCHC = 3

	USCSACCHNS   = 0 // SACCH non-superframe: header/trailer frames
	USCUDCH      = 1
	USCSACCHSS   = 2 // SACCH superframe: voice frames
	USCSACCHIdle = 3

	StealNone         = 3 // both halves voice
	StealFACCH1Second = 2 // second half FACCH1 (first half voice)
	StealFACCH1First  = 1 // first half FACCH1 (second half voice)
	StealFACCH        = 0 // both halves FACCH1

	DirInbound  = 0
	DirOutbound = 1
)

// LICH is the 8 information bits of the Link Information Channel: RFCT(2),
// USC(2), option(2), direction(1), parity(1). Parity follows MMDVMHost
// (NXDNLICH.cpp getParity): set when the top nibble is 0x8 or 0xB.
type LICH byte

// NewLICH builds a LICH with parity.
func NewLICH(rfct, usc, option, dir int) LICH {
	l := LICH(byte(rfct&3)<<6 | byte(usc&3)<<4 | byte(option&3)<<2 | byte(dir&1)<<1)
	return l.WithParity()
}

func (l LICH) parity() bool {
	switch byte(l) & 0xF0 {
	case 0x80, 0xB0:
		return true
	}
	return false
}

// WithParity returns l with its parity bit set as MMDVMHost does.
func (l LICH) WithParity() LICH {
	if l.parity() {
		return l | 1
	}
	return l &^ 1
}

// ParityOK reports whether the parity bit of l is consistent.
func (l LICH) ParityOK() bool { return (byte(l)&1 == 1) == l.parity() }

func (l LICH) RFCT() int      { return int(l>>6) & 3 }
func (l LICH) USC() int       { return int(l>>4) & 3 }
func (l LICH) Option() int    { return int(l>>2) & 3 }
func (l LICH) Direction() int { return int(l>>1) & 1 }

// Header/trailer and voice-body LICH bytes sent towards reflectors.
var (
	LICHHeader = NewLICH(RFCTRDCH, USCSACCHNS, StealFACCH, DirInbound) // 0x81
	LICHVoice  = NewLICH(RFCTRDCH, USCSACCHSS, StealNone, DirInbound)  // 0xAC
)

// Frame geometry in bits.
const (
	FSWBits   = 20
	LICHBits  = 16
	SACCHBits = 60
	FACCHBits = 144
	FrameBits = 384
	// FrameBytes is a whole RF frame.
	FrameBytes = FrameBits / 8
	// Offsets of the two FACCH1 slots in an RF frame.
	FACCH1Offset1 = FSWBits + LICHBits + SACCHBits
	FACCH1Offset2 = FSWBits + LICHBits + SACCHBits + FACCHBits
)

// Encode writes the LICH into an RF frame (data points at the frame, FSW
// first): each of the 8 bits is followed by a 1 bit, from bit 20.
func (l LICH) Encode(frame []byte) {
	off := FSWBits
	for i := 0; i < LICHBits/2; i++ {
		setBit(frame, off, l>>(7-uint(i))&1 != 0)
		off++
		setBit(frame, off, true)
		off++
	}
}

// DecodeLICH reads the LICH from an RF frame and reports parity validity.
func DecodeLICH(frame []byte) (LICH, bool) {
	var l LICH
	off := FSWBits
	for i := 0; i < LICHBits/2; i++ {
		if getBit(frame, off) {
			l |= 1 << (7 - uint(i))
		}
		off += 2
	}
	return l, l.ParityOK()
}
