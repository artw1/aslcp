package dmr

// BPTC(196,96) (ETSI TS 102 361-1 B.1.1): 96 info bits in a 13x15 matrix
// (rows Hamming(15,11,3), columns Hamming(13,9,3), plus three reserved bits),
// interleaved with index (a*181) mod 196 and carried in burst bits 0..97 and
// 166..263. Layout as in MMDVMHost BPTC19696.cpp (G4KLX, GPL-2.0-or-later).

// bptcPos maps bit j (0..195) of the BPTC block to its burst position.
func bptcPos(j int) int {
	if j < 98 {
		return j
	}
	return j + 68
}

// bptcInfo lists the matrix positions (index into the 196-bit
// deinterleaved block) of the 96 info bits.
var bptcInfo [96]int

func init() {
	k := 0
	for i := 4; i <= 11; i++ {
		bptcInfo[k] = i
		k++
	}
	for r := 1; r < 9; r++ {
		for i := r*15 + 1; i <= r*15+11; i++ {
			bptcInfo[k] = i
			k++
		}
	}
}

// PutBPTC196 encodes 96 info bits into the BPTC(196,96) field of b.
func (b *Burst) PutBPTC196(info [12]byte) {
	var m [196]bool
	for k, p := range bptcInfo {
		m[p] = getBit(info[:], k)
	}
	for r := 0; r < 9; r++ {
		hamming15113.encode(m[r*15+1 : r*15+16])
	}
	var col [13]bool
	for c := 0; c < 15; c++ {
		for a := 0; a < 13; a++ {
			col[a] = m[c+1+15*a]
		}
		hamming1393.encode(col[:])
		for a := 0; a < 13; a++ {
			m[c+1+15*a] = col[a]
		}
	}
	for a := 0; a < 196; a++ {
		setBit(b[:], bptcPos((a*181)%196), m[a])
	}
}

// BPTC196 decodes the BPTC(196,96) field of b, iterating row and column
// single-error correction. fixed is the number of bits changed; ok is false
// when a row or column check still fails.
func (b *Burst) BPTC196() (info [12]byte, fixed int, ok bool) {
	var m [196]bool
	for a := 0; a < 196; a++ {
		m[a] = getBit(b[:], bptcPos((a*181)%196))
	}
	var col [13]bool
	for pass := 0; pass < 5; pass++ {
		changed := false
		for c := 0; c < 15; c++ {
			for a := 0; a < 13; a++ {
				col[a] = m[c+1+15*a]
			}
			if f, _ := hamming1393.decode(col[:]); f {
				changed = true
				fixed++
				for a := 0; a < 13; a++ {
					m[c+1+15*a] = col[a]
				}
			}
		}
		for r := 0; r < 9; r++ {
			if f, _ := hamming15113.decode(m[r*15+1 : r*15+16]); f {
				changed = true
				fixed++
			}
		}
		if !changed {
			break
		}
	}
	ok = true
	for r := 0; r < 9 && ok; r++ {
		ok = hamming15113.syndrome(m[r*15+1:r*15+16]) == 0
	}
	for c := 0; c < 15 && ok; c++ {
		for a := 0; a < 13; a++ {
			col[a] = m[c+1+15*a]
		}
		ok = hamming1393.syndrome(col[:]) == 0
	}
	for k, p := range bptcInfo {
		setBit(info[:], k, m[p])
	}
	return info, fixed, ok
}
