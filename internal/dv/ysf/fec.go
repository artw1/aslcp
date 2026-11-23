// SPDX-License-Identifier: GPL-3.0-or-later

package ysf

import "math/bits"

// ---- Golay(24,12) ----
//
// The extended Golay code of MMDVMHost Golay24128.cpp: a Golay(23,12)
// codeword (generator 0xC75, data in the top 12 bits) shifted left by one
// with an even-parity bit appended. The encoding and syndrome tables are
// computed here instead of being copied.

const golayPoly = 0xC75

var golaySyndrome [2048]uint32 // syndrome -> error pattern (23 bits), weight <= 3

func golay23Syndrome(p uint32) uint32 {
	for i := 22; i >= 11; i-- {
		if p&(1<<uint(i)) != 0 {
			p ^= golayPoly << uint(i-11)
		}
	}
	return p & 0x7FF
}

func init() {
	for i := range golaySyndrome {
		golaySyndrome[i] = 0xFFFFFFFF
	}
	set := func(e uint32) {
		s := golay23Syndrome(e)
		if golaySyndrome[s] == 0xFFFFFFFF {
			golaySyndrome[s] = e
		}
	}
	set(0)
	for a := 0; a < 23; a++ {
		set(1 << uint(a))
		for b := a + 1; b < 23; b++ {
			set(1<<uint(a) | 1<<uint(b))
			for c := b + 1; c < 23; c++ {
				set(1<<uint(a) | 1<<uint(b) | 1<<uint(c))
			}
		}
	}
}

// Golay24Encode returns the 24-bit codeword of the 12-bit value d.
func Golay24Encode(d uint32) uint32 {
	d &= 0xFFF
	c23 := d<<11 | golay23Syndrome(d<<11)
	return c23<<1 | uint32(bits.OnesCount32(c23)&1)
}

// Golay24Decode corrects a 24-bit codeword and returns its 12 data bits.
// ok is false for an uncorrectable word, with MMDVMHost's decode24128
// criterion (syndrome weight below 3, or even parity after correction).
func Golay24Decode(c uint32) (d uint32, ok bool) {
	c &= 0xFFFFFF
	s := golay23Syndrome(c >> 1)
	out := c ^ golaySyndrome[s]<<1
	ok = bits.OnesCount32(s) < 3 || bits.OnesCount32(out)&1 == 0
	return out >> 12, ok
}

// ---- CRC-CCITT ----
//
// MMDVMHost CRC.cpp addCCITT162/checkCCITT162: polynomial 0x1021, initial
// value 0, MSB first, result inverted, stored big-endian in the last two
// bytes.

func crc16(p []byte) uint16 {
	var crc uint16
	for _, b := range p {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return ^crc
}

// addCRC writes the CRC of p[:len(p)-2] into the last two bytes.
func addCRC(p []byte) {
	c := crc16(p[:len(p)-2])
	p[len(p)-2], p[len(p)-1] = byte(c>>8), byte(c)
}

func checkCRC(p []byte) bool {
	c := crc16(p[:len(p)-2])
	return p[len(p)-2] == byte(c>>8) && p[len(p)-1] == byte(c)
}

// ---- rate 1/2, K=5 convolutional code ----
//
// Encoder and hard-decision Viterbi decoder as in MMDVMHost
// YSFConvolution.cpp (G1 = 1 + D^3 + D^4, G2 = 1 + D + D^2 + D^4).

func convEncode(in, out []byte, nBits int) {
	var d1, d2, d3, d4 uint8
	k := 0
	for i := 0; i < nBits; i++ {
		d := b2u(getBit(in, i))
		g1 := (d + d3 + d4) & 1
		g2 := (d + d1 + d2 + d4) & 1
		d4, d3, d2, d1 = d3, d2, d1, d
		setBit(out, k, g1 != 0)
		setBit(out, k+1, g2 != 0)
		k += 2
	}
}

var (
	branch1 = [8]uint8{0, 0, 0, 0, 1, 1, 1, 1}
	branch2 = [8]uint8{0, 1, 1, 0, 0, 1, 1, 0}
)

type viterbi struct {
	old, cur [16]uint16
	dec      []uint64
}

func (v *viterbi) start(steps int) {
	v.old, v.cur = [16]uint16{}, [16]uint16{}
	v.dec = make([]uint64, 0, steps)
}

func (v *viterbi) decode(s0, s1 uint8) {
	var d uint64
	for i := 0; i < 8; i++ {
		j := uint(i * 2)
		metric := uint16((branch1[i] ^ s0) + (branch2[i] ^ s1))
		m0 := v.old[i] + metric
		m1 := v.old[i+8] + (2 - metric)
		if m0 >= m1 {
			v.cur[j] = m1
			d |= 1 << j
		} else {
			v.cur[j] = m0
		}
		m0 = v.old[i] + (2 - metric)
		m1 = v.old[i+8] + metric
		if m0 >= m1 {
			v.cur[j+1] = m1
			d |= 1 << (j + 1)
		} else {
			v.cur[j+1] = m0
		}
	}
	v.dec = append(v.dec, d)
	v.old, v.cur = v.cur, v.old
}

// chainback writes nBits decoded bits to out, tracing back from the last
// decision (MMDVMHost's chainback, including its state arithmetic).
func (v *viterbi) chainback(out []byte, nBits int) {
	var state uint32
	p := len(v.dec)
	for nBits > 0 {
		nBits--
		p--
		bit := uint32(v.dec[p]>>(state>>4)) & 1
		state = bit<<7 | state>>1
		setBit(out, nBits, bit != 0)
	}
}
