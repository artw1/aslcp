package nxdn

// SACCH structure field values (MMDVMHost NXDNDefines.h NXDN_SR_*). On a
// superframe the four voice frames carry SR 3,2,1,0 (parts 1/4..4/4 of the
// Layer 3 message); a lone SACCH uses SRSingle.
const (
	SRSingle = 0
	SR44     = 0
	SR34     = 1
	SR24     = 2
	SR14     = 3
)

// SACCHIdle is the 18-bit idle message (type IDLE 0x10), left aligned in 3
// bytes.
var SACCHIdle = [3]byte{0x10, 0x00, 0x00}

// SACCH is the 26 information bits of a SACCH: structure(2) RAN(6) data(18).
// Raw is those 26 bits followed by the CRC-6 (32 bits = the 4 bytes at
// offset 1 of a network payload).
type SACCH struct{ b [4]byte }

// NewSACCH builds a SACCH from its fields; data holds 18 bits, left aligned.
func NewSACCH(structure, ran int, data [3]byte) SACCH {
	var s SACCH
	s.b[0] = byte(structure&3)<<6 | byte(ran&0x3F)
	for i := 0; i < 18; i++ {
		setBit(s.b[:], 8+i, getBit(data[:], i))
	}
	return s
}

// SACCHFromRaw wraps the 4 raw bytes of a payload (CRC not checked).
func SACCHFromRaw(raw []byte) SACCH {
	var s SACCH
	copy(s.b[:], raw[:4])
	return s
}

func (s SACCH) RAN() int       { return int(s.b[0] & 0x3F) }
func (s SACCH) Structure() int { return int(s.b[0]>>6) & 3 }

// Data returns the 18 data bits, left aligned in 3 bytes.
func (s SACCH) Data() (d [3]byte) {
	for i := 0; i < 18; i++ {
		setBit(d[:], i, getBit(s.b[:], 8+i))
	}
	return d
}

// Raw returns the 4 bytes with the CRC-6 recomputed.
func (s SACCH) Raw() [4]byte {
	r := s.b
	EncodeCRC6(r[:], 26)
	return r
}

// SACCHCRCOK reports whether the 4 raw bytes carry a valid CRC-6.
func SACCHCRCOK(raw []byte) bool { return CheckCRC6(raw, 26) }

var (
	sacchInterleave [SACCHBits]int
	sacchPuncture   = [...]int{5, 11, 17, 23, 29, 35, 41, 47, 53, 59, 65, 71}
)

func init() {
	for i := range sacchInterleave {
		sacchInterleave[i] = (i%12)*5 + i/12
	}
}

// Encode writes the SACCH (convolutional code, puncturing, interleave) into
// an RF frame at bit 36 (after FSW and LICH).
func (s SACCH) Encode(frame []byte) {
	var t1 [5]byte
	for i := 0; i < 26; i++ {
		setBit(t1[:], i, getBit(s.b[:], i))
	}
	EncodeCRC6(t1[:], 26)
	t2 := ConvEncode(t1[:], 36) // 72 bits
	var t3 [8]byte
	n, idx := 0, 0
	for i := 0; i < 72; i++ {
		if idx < len(sacchPuncture) && i == sacchPuncture[idx] {
			idx++
			continue
		}
		setBit(t3[:], n, getBit(t2, i))
		n++
	}
	for i := 0; i < SACCHBits; i++ {
		setBit(frame, sacchInterleave[i]+FSWBits+LICHBits, getBit(t3[:], i))
	}
}

// DecodeSACCH reads the SACCH of an RF frame; ok reports the CRC-6.
func DecodeSACCH(frame []byte) (s SACCH, ok bool) {
	var t1 [8]byte
	for i := 0; i < SACCHBits; i++ {
		setBit(t1[:], i, getBit(frame, sacchInterleave[i]+FSWBits+LICHBits))
	}
	var t2 [90]uint8
	n, idx := 0, 0
	for i := 0; i < SACCHBits; i++ {
		if idx < len(sacchPuncture) && n == sacchPuncture[idx] {
			t2[n] = 1
			n++
			idx++
		}
		if getBit(t1[:], i) {
			t2[n] = 2
		}
		n++
	}
	// the last 8 symbols stay 0 (tail)
	var v Viterbi
	v.Start()
	n = 0
	for i := 0; i < 40; i++ {
		v.Decode(t2[n], t2[n+1])
		n += 2
	}
	var out [5]byte
	v.Chainback(out[:], 36)
	copy(s.b[:], out[:4])
	return s, CheckCRC6(out[:], 26)
}
