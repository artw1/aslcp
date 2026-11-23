// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// src/quantise.c, inc/quantise.h at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

// binaryToGray and grayToBinary are the Gray code helpers of quantise.c.
func binaryToGray(x uint) uint { return x ^ (x >> 1) }

func grayToBinary(g uint) uint {
	g ^= g >> 16
	g ^= g >> 8
	g ^= g >> 4
	g ^= g >> 2
	g ^= g >> 1
	return g
}

// pack appends the low width bits of field (Gray coded if width > 1) to
// bits, MSB first, at bit position *idx. bits must be zeroed beforehand.
func pack(bits *[BytesPerFrame]byte, idx *uint, field, width uint) {
	if width > 1 {
		field = binaryToGray(field)
	}
	for {
		bI := *idx
		bitsLeft := 8 - (bI & 7)
		slice := width
		if bitsLeft < slice {
			slice = bitsLeft
		}
		bits[bI>>3] |= byte((field >> (width - slice)) << (bitsLeft - slice))
		*idx = bI + slice
		width -= slice
		if width == 0 {
			break
		}
	}
}

// unpack reads width bits from bits at *idx (Gray decoded if width > 1).
func unpack(bits *[BytesPerFrame]byte, idx *uint, width uint) int {
	mask8 := [9]uint{0x00, 0x01, 0x03, 0x07, 0x0F, 0x1F, 0x3F, 0x7F, 0xFF}
	var field uint
	orig := width
	for {
		bI := *idx
		bitsLeft := 8 - (bI & 7)
		slice := width
		if bitsLeft < slice {
			slice = bitsLeft
		}
		field |= ((uint(bits[bI>>3]) >> (bitsLeft - slice)) & mask8[slice]) << (width - slice)
		*idx = bI + slice
		width -= slice
		if width == 0 {
			break
		}
	}
	if orig > 1 {
		return int(grayToBinary(field))
	}
	return int(field)
}

// encodeWo quantises the fundamental frequency (encode_Wo).
func encodeWo(wo float32, bits uint) int {
	levels := 1 << bits
	norm := float32((float64(wo) - w0Min) / (w0Max - w0Min))
	index := int(float32(float32(levels)*norm) + 0.5)
	if index < 0 {
		index = 0
	} else if index > levels-1 {
		index = levels - 1
	}
	return index
}

// decodeWo is decode_Wo.
func decodeWo(index int, bits uint) float32 {
	levels := 1 << bits
	step := float32((w0Max - w0Min) / float64(levels))
	return float32(w0Min + float64(float32(step*float32(index))))
}

// encodeEnergy quantises the LPC energy in dB (encode_energy).
func encodeEnergy(e float32, bits uint) int {
	levels := 1 << bits
	const eMin, eMax = float32(-10.0), float32(40.0)
	if !(e > 1e-12) { // fmaxf(e, 1e-12f), NaN-safe
		e = 1e-12
	}
	eDB := float32(10.0 * log10f(e))
	norm := (eDB - eMin) / (eMax - eMin)
	index := int(float32(float32(levels)*norm) + 0.5)
	if index < 0 {
		index = 0
	} else if index > levels-1 {
		index = levels - 1
	}
	return index
}

// decodeEnergy is decode_energy.
func decodeEnergy(index int, bits uint) float32 {
	levels := 1 << bits
	step := float32((40.0 - -10.0) / float64(levels))
	eDB := float32(-10.0 + float64(float32(step*float32(index))))
	return pow10f(eDB / 10.0)
}

// encodeLSPDScalar is encode_lspds_scalar: delta-LSP scalar quantiser.
func encodeLSPDScalar(indexes *[lspOrd]int, lsp *[lspOrd]float32) {
	lastQHz := 0
	for i := 0; i < lspOrd; i++ {
		cb := &deltaLSPCB[i]
		lspHz := float32(lspEncK * lsp[i])
		target := lspHz
		if i != 0 {
			target = lspHz - float32(lastQHz)
		}
		var bestJ int
		switch {
		case target <= float32(cb[0]):
			bestJ = 0
		case target >= float32(cb[31]):
			bestJ = 31
		default:
			lo, hi := 0, 31
			for lo+1 < hi {
				mid := (lo + hi) >> 1
				if float32(cb[mid]) <= target {
					lo = mid
				} else {
					hi = mid
				}
			}
			eLo := fabsf(float32(cb[lo]) - target)
			eHi := fabsf(float32(cb[hi]) - target)
			if eLo <= eHi {
				bestJ = lo
			} else {
				bestJ = hi
			}
		}
		indexes[i] = bestJ
		if i == 0 {
			lastQHz = int(cb[bestJ])
		} else {
			lastQHz += int(cb[bestJ])
		}
	}
}

// decodeLSPDScalar is decode_lspds_scalar.
func decodeLSPDScalar(lsp *[lspOrd]float32, indexes *[lspOrd]int) {
	hz := 0
	for i := 0; i < lspOrd; i++ {
		hz += int(deltaLSPCB[i][indexes[i]])
		lsp[i] = float32(lspDecK * float32(hz))
	}
}
