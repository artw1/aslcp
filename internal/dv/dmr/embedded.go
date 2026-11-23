package dmr

// Embedded LC (ETSI TS 102 361-1 B.2.1 and 9.1.6.1): the 72-bit LC plus a
// 5-bit checksum (sum of the 9 bytes mod 31) in a 7x16 matrix of
// Hamming(16,11,4) rows plus a column-parity row, read out column-wise into
// four 32-bit fragments carried by voice bursts B, C, D and E. Layout as in
// MMDVMHost DMREmbeddedData.cpp (G4KLX, GPL-2.0-or-later).

// embRuns are the data positions in the 128-bit matrix (row-major, 16 per row).
var embRuns = [][2]int{{0, 11}, {16, 27}, {32, 42}, {48, 58}, {64, 74}, {80, 90}, {96, 106}}

// embCRC are the matrix positions of checksum bits 4..0.
var embCRC = [5]int{42, 58, 74, 90, 106}

func embeddedChecksum(lc [LCLen]byte) uint8 {
	s := 0
	for _, c := range lc {
		s += int(c)
	}
	return uint8(s % 31)
}

// embRaw returns the transmit (column-wise) index of matrix position m.
var embOrder [128]int // embOrder[a] = matrix position sent as raw bit a

func init() {
	b := 0
	for a := 0; a < 128; a++ {
		embOrder[a] = b
		b += 16
		if b > 127 {
			b -= 127
		}
	}
}

// EmbeddedLC encodes lc into the four 32-bit fragments of bursts B, C, D, E
// (to be sent with LCSS First, Continue, Continue, Last).
func EmbeddedLC(lc LC) (frags [4][4]byte) {
	raw := lc.Bytes()
	var m [128]bool
	k := 0
	for _, r := range embRuns {
		for i := r[0]; i < r[1]; i++ {
			m[i] = getBit(raw[:], k)
			k++
		}
	}
	crc := embeddedChecksum(raw)
	for i, p := range embCRC {
		m[p] = crc&(0x10>>i) != 0
	}
	for r := 0; r < 112; r += 16 {
		hamming16114.encode(m[r : r+16])
	}
	for c := 0; c < 16; c++ {
		v := false
		for r := 0; r < 112; r += 16 {
			v = v != m[r+c]
		}
		m[112+c] = v
	}
	for a := 0; a < 128; a++ {
		setBit(frags[a/32][:], a%32, m[embOrder[a]])
	}
	return frags
}

// DecodeEmbeddedLC decodes the four fragments of an embedded LC, correcting
// one bit error per row. It fails when a row or column check fails or the
// checksum does not match.
func DecodeEmbeddedLC(frags [4][4]byte) (LC, bool) {
	var m [128]bool
	for a := 0; a < 128; a++ {
		m[embOrder[a]] = getBit(frags[a/32][:], a%32)
	}
	for r := 0; r < 112; r += 16 {
		if _, ok := hamming16114.decode(m[r : r+16]); !ok {
			return LC{}, false
		}
	}
	for c := 0; c < 16; c++ {
		v := false
		for r := 0; r < 128; r += 16 {
			v = v != m[r+c]
		}
		if v {
			return LC{}, false
		}
	}
	var raw [LCLen]byte
	k := 0
	for _, r := range embRuns {
		for i := r[0]; i < r[1]; i++ {
			setBit(raw[:], k, m[i])
			k++
		}
	}
	var crc uint8
	for i, p := range embCRC {
		if m[p] {
			crc |= 0x10 >> i
		}
	}
	if crc != embeddedChecksum(raw) {
		return LC{}, false
	}
	lc, _ := ParseLC(raw[:])
	return lc, true
}
