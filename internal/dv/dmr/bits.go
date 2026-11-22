package dmr

import "math/bits"

// getBit returns bit i (0 = MSB of p[0]) of p.
func getBit(p []byte, i int) bool { return p[i>>3]&(0x80>>(i&7)) != 0 }

// setBit sets bit i (0 = MSB of p[0]) of p to v.
func setBit(p []byte, i int, v bool) {
	if v {
		p[i>>3] |= 0x80 >> (i & 7)
	} else {
		p[i>>3] &^= 0x80 >> (i & 7)
	}
}

// getBits reads n bits (n <= 32) from position i, MSB first.
func getBits(p []byte, i, n int) uint32 {
	var v uint32
	for k := 0; k < n; k++ {
		v <<= 1
		if getBit(p, i+k) {
			v |= 1
		}
	}
	return v
}

// putBits writes the low n bits of v at position i, MSB first.
func putBits(p []byte, i, n int, v uint32) {
	for k := 0; k < n; k++ {
		setBit(p, i+k, v&(1<<(n-1-k)) != 0)
	}
}

func popcount(v uint32) int { return bits.OnesCount32(v) }
