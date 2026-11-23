// SPDX-License-Identifier: GPL-3.0-or-later
//
// Frame packing and the P25 Phase 1 IMBE channel coding (Golay(23,12),
// Hamming(15,11) and the PN scrambler). The FEC is a Go port of the
// algorithms in OP25's op25_imbe_frame.h, op25_golay.h and op25_hamming.h
// (GPL-3.0-or-later); the Golay syndrome table is regenerated rather than
// copied and has been checked entry-for-entry against OP25's.

package imbe

import "math/bits"

// Vectors holds the eight IMBE information vectors u0..u7 of one frame,
// exactly as the reference's frame_vector[]: u0..u3 are 12 bits, u4..u6 are
// 11 bits and u7 is 7 bits (right-aligned, NOT OP25's left-shifted form).
type Vectors [8]uint16

// vectorWidths are the widths in bits of u0..u7.
var vectorWidths = [8]uint{12, 12, 12, 12, 11, 11, 11, 7}

// PackVectors concatenates u0..u7 MSB first into 88 bits (11 bytes). Bits
// above each vector's width are ignored.
func PackVectors(v Vectors) [11]byte {
	var b [11]byte
	pos := 0
	for i, w := range vectorWidths {
		x := uint32(v[i]) & (1<<w - 1)
		for k := int(w) - 1; k >= 0; k-- {
			if x>>uint(k)&1 != 0 {
				b[pos/8] |= 0x80 >> uint(pos%8)
			}
			pos++
		}
	}
	return b
}

// UnpackVectors is the inverse of PackVectors.
func UnpackVectors(b *[11]byte) Vectors {
	var v Vectors
	pos := 0
	for i, w := range vectorWidths {
		var x uint16
		for k := uint(0); k < w; k++ {
			x = x<<1 | uint16(b[pos/8]>>(7-uint(pos%8))&1)
			pos++
		}
		v[i] = x
	}
	return v
}

// Int16 returns v in the reference's Word16 frame_vector form.
func (v Vectors) Int16() [8]int16 {
	var r [8]int16
	for i, x := range v {
		r[i] = int16(x)
	}
	return r
}

// VectorsFromInt16 converts a reference frame_vector to Vectors.
func VectorsFromInt16(fv *[8]int16) Vectors {
	var v Vectors
	for i, x := range fv {
		v[i] = uint16(x)
	}
	return v
}

// golay23Encode returns the systematic Golay(23,12) codeword of u: data in
// bits 22..11, parity (u<<11 mod 0xC75) in bits 10..0. This equals OP25's
// golay_23_encode (golay_24_encode(u) >> 1).
func golay23Encode(u uint32) uint32 {
	u &= 0xFFF
	return u<<11 | golay23Syndrome(u<<11)
}

// golay23Syndrome reduces a 23-bit pattern modulo the Golay generator
// polynomial (golay_23_syndrome).
func golay23Syndrome(pattern uint32) uint32 {
	aux := uint32(0x400000)
	for pattern&0xFFFFF800 != 0 {
		for aux&pattern == 0 {
			aux >>= 1
		}
		pattern ^= (aux >> 11) * 0xC75
	}
	return pattern
}

// golayDecoding maps a syndrome to (data-bit error pattern | error count),
// identical to the decoding[2048] table in golay_23_decode.
var golayDecoding = func() (t [2048]uint32) {
	// Every error pattern of weight <= 3 has a distinct syndrome (the code
	// is perfect), so this fills all 2048 entries.
	set := func(p uint32) {
		t[golay23Syndrome(p)] = p&^0x7FF | uint32(bits.OnesCount32(p))
	}
	set(0)
	for a := uint(0); a < 23; a++ {
		set(1 << a)
		for b := a + 1; b < 23; b++ {
			set(1<<a | 1<<b)
			for c := b + 1; c < 23; c++ {
				set(1<<a | 1<<b | 1<<c)
			}
		}
	}
	return
}()

// golay23Decode corrects up to three errors and returns the 12 data bits
// and the number of errors corrected (golay_23_decode).
func golay23Decode(cw uint32) (uint32, int) {
	cw &= 0x7FFFFF
	correction := golayDecoding[golay23Syndrome(cw)]
	cw ^= correction
	return cw >> 11, int(correction & 3)
}

// hammingEncoding is hamming_15_encode's generator table (bits 14..4 data,
// 3..0 parity).
var hammingEncoding = [11]uint16{0x400f, 0x200e, 0x100d, 0x080c, 0x040b, 0x020a, 0x0109, 0x0087, 0x0046, 0x0025, 0x0013}

// hammingDecoding maps a syndrome to the data-bit correction.
var hammingDecoding = [16]uint16{0, 0, 0, 1, 0, 2, 4, 8, 0, 16, 32, 64, 128, 256, 512, 1024}

// hamming15Encode is hamming_15_encode.
func hamming15Encode(u uint16) uint16 {
	var o uint16
	for i := 0; i < 11; i++ {
		if u&(1<<uint(10-i)) != 0 {
			o ^= hammingEncoding[i]
		}
	}
	return o
}

// hamming15Decode is hamming_15_decode: it returns the 11 data bits and 1
// if a data bit was corrected (an error confined to the parity bits is
// counted as 0, as in OP25).
func hamming15Decode(cw uint16) (uint16, int) {
	par := cw & 0xF
	data := (cw & 0x7FFF) >> 4
	if c := hammingDecoding[(hamming15Encode(data)&0xF)^par]; c != 0 {
		return data ^ c, 1
	}
	return data, 0
}

// pnGen returns the next n bits of the P25 IMBE PN sequence (pngen23 for
// n=23, pngen15 for n=15).
func pnGen(pr *uint32, n int) uint32 {
	var r uint32
	for i := n - 1; i >= 0; i-- {
		*pr = (173**pr + 13849) & 0xFFFF
		if *pr&32768 != 0 {
			r |= 1 << uint(i)
		}
	}
	return r
}

// putBits/getBits address the 144-bit codeword MSB first (codeword bit
// 0 is the MSB of byte 0), matching OP25's voice_codeword bit order.
func putBits(cw *[18]byte, begin, end int, v uint32) {
	for i := begin; i < end; i++ {
		if v>>uint(end-1-i)&1 != 0 {
			cw[i/8] |= 0x80 >> uint(i%8)
		} else {
			cw[i/8] &^= 0x80 >> uint(i%8)
		}
	}
}

func getBits(cw *[18]byte, begin, end int) uint32 {
	var v uint32
	for i := begin; i < end; i++ {
		v = v<<1 | uint32(cw[i/8]>>(7-uint(i%8))&1)
	}
	return v
}

// EncodeFEC produces the 144-bit FEC-protected IMBE codeword (before the
// LDU interleave) from u0..u7, as OP25's imbe_header_encode does when given
// u7 in its shifted form (u7<<1):
//
//	bits   0..22   Golay(23,12)(u0)
//	bits  23..45   Golay(23,12)(u1) ^ PN
//	bits  46..68   Golay(23,12)(u2) ^ PN
//	bits  69..91   Golay(23,12)(u3) ^ PN
//	bits  92..106  Hamming(15,11)(u4) ^ PN
//	bits 107..121  Hamming(15,11)(u5) ^ PN
//	bits 122..136  Hamming(15,11)(u6) ^ PN
//	bits 137..143  u7 (7 bits, uncoded)
//
// The PN generator is seeded with u0<<4. Bits are MSB first within the
// result and within each field.
func EncodeFEC(v Vectors) [18]byte {
	var cw [18]byte
	pn := uint32(v[0]&0xFFF) << 4
	putBits(&cw, 0, 23, golay23Encode(uint32(v[0])))
	putBits(&cw, 23, 46, golay23Encode(uint32(v[1]))^pnGen(&pn, 23))
	putBits(&cw, 46, 69, golay23Encode(uint32(v[2]))^pnGen(&pn, 23))
	putBits(&cw, 69, 92, golay23Encode(uint32(v[3]))^pnGen(&pn, 23))
	putBits(&cw, 92, 107, uint32(hamming15Encode(v[4]&0x7FF))^pnGen(&pn, 15))
	putBits(&cw, 107, 122, uint32(hamming15Encode(v[5]&0x7FF))^pnGen(&pn, 15))
	putBits(&cw, 122, 137, uint32(hamming15Encode(v[6]&0x7FF))^pnGen(&pn, 15))
	putBits(&cw, 137, 144, uint32(v[7]&0x7F))
	return cw
}

// DecodeFEC error-corrects a 144-bit codeword (see EncodeFEC) and returns
// u0..u7 (u7 right-aligned, 7 bits) together with OP25's error counts: e0
// is the number of errors corrected in u0, et the total over u0..u6
// (imbe_header_decode).
func DecodeFEC(cw *[18]byte) (v Vectors, e0, et int) {
	u0, n := golay23Decode(getBits(cw, 0, 23))
	v[0] = uint16(u0)
	e0, et = n, n
	pn := u0 << 4
	for i, f := range [3][2]int{{23, 46}, {46, 69}, {69, 92}} {
		u, n := golay23Decode(getBits(cw, f[0], f[1]) ^ pnGen(&pn, 23))
		v[1+i] = uint16(u)
		et += n
	}
	for i, f := range [3][2]int{{92, 107}, {107, 122}, {122, 137}} {
		u, n := hamming15Decode(uint16(getBits(cw, f[0], f[1]) ^ pnGen(&pn, 15)))
		v[4+i] = u
		et += n
	}
	v[7] = uint16(getBits(cw, 137, 144))
	return
}
