// SPDX-License-Identifier: GPL-3.0-or-later

// Package imbe is a pure-Go, bit-exact port of Pavel Yazev's fixed-point
// P25 Phase 1 IMBE vocoder ("imbe_vocoder", as shipped in OP25:
// https://github.com/boatbod/op25, op25/gr-op25_repeater/lib/imbe_vocoder,
// commit 38a37491df4532d932f9f1d5634617af410cb3c9).
//
// # Licence
//
// The original is "Project 25 IMBE Encoder/Decoder Fixed-Point
// implementation, Developed by Pavel Yazev, Version 1.0 (c) Copyright 2009"
// and is distributed under the GNU General Public License version 3 or
// later. This package is a derivative work under the same licence; every
// ported file keeps the original copyright and licence notice, and the
// original C function names are given in the doc comments. The FEC helpers
// in fec.go port algorithms from OP25 (also GPL-3.0-or-later).
//
// # Usage
//
// Encoder and Decoder are independent and stateful (one per stream); they
// are not safe for concurrent use. Encode takes 160 samples of 8 kHz,
// 16-bit linear PCM (20 ms) and returns one 88-bit frame; Decode does the
// reverse. All state that the reference keeps in globals or class members
// lives in these structs; the package has no mutable globals (the FFT
// twiddle and Golay tables are computed once at init and only read).
//
// # Frame format
//
// The reference works on frame_vector[8], the IMBE information vectors
// u0..u7 (see Vectors):
//
//	u0..u3  12 bits each (protected by Golay(23,12) on the air)
//	u4..u6  11 bits each (protected by Hamming(15,11) on the air)
//	u7       7 bits      (uncoded)
//
// The 11-byte form used by Encode/Decode is u0, u1, ..., u7 concatenated
// MSB first: 4*12 + 3*11 + 7 = 88 bits, no padding. Byte 0 holds u0 bits
// 11..4, byte 1 holds u0 bits 3..0 and u1 bits 11..8, and so on; the last
// byte ends with u6 bit 0 followed by u7 bits 6..0. This is the same byte
// layout as OP25's imbe_pack/imbe_unpack, except that OP25 carries u7 as
// an 8-bit value shifted left by one (bit 0 unused) whereas Vectors and the
// reference vocoder use the plain 7-bit value. Within u7 the reference
// places: 0x40, 0x20, 0x10 = the last three bits of the prioritised bit
// stream; 0x08 = LSB of b2; 0x06 = the two LSBs of b0 (pitch); 0x01 = the
// synchronisation bit b(L+1).
//
// PackVectors/UnpackVectors convert between the 11-byte form and Vectors;
// Vectors.Int16 and VectorsFromInt16 convert to and from the reference's
// Word16 frame_vector. EncodeFEC/DecodeFEC convert between Vectors and the
// 144-bit FEC-protected codeword (Golay/Hamming plus the PN scrambler
// seeded from u0, before the LDU interleave), bit-exact with OP25's
// imbe_header_encode/imbe_header_decode, including its error counts. The
// voice-frame interleave within an LDU (OP25's voice_codeword_bits table)
// is out of scope.
//
// # Conformance and caveats
//
//   - Encode and Decode outputs are verified bit-exact against the C++
//     reference (see testdata/README.md) on a synthetic signal covering
//     sines with noise, clipping, digital silence, a chirp and a voiced
//     source with vibrato alternating with noise, plus decoding of random
//     frames.
//   - rand_gen() in the reference uses a file-static seed shared by every
//     imbe_vocoder instance in a process, and each constructor draws 56
//     values from it. Each Decoder here has its own generator seeded as
//     the reference's first instance is, so output matches the reference
//     only when the reference process uses a single instance. With several
//     instances in one C++ process the reference's output depends on the
//     order in which they were created and used; this port's does not.
//   - The ETSI Overflow and Carry flags are written by the reference but
//     never read, and the ops that read them (L_add_c, L_sub_c, L_macNs,
//     L_msuNs, L_sat) are never called, so they are not ported.
//   - div_s aborts the process in the reference on invalid operands; here
//     it returns 0 (for a < 0 or b <= 0) or MAX_16 (for a >= b). The
//     tests never reach that path.
//   - The reference's C signed-overflow wraparound (e.g. in v_synt's phase
//     accumulators and pitch_est/e_p's plain multiplies) is reproduced
//     with Go's defined two's-complement wrapping.
//   - The encoder's synchronisation bit (u7 bit 0) is taken, as in the
//     reference, from b_vec[num_harms + 2] of the encoder's own parameter
//     block, which the encoder never sets for the current frame (it holds
//     a quantised coefficient left over from an earlier frame with more
//     harmonics). This is ported faithfully.
//   - OP25's own transmit path (p25p1_voice_encode) passes the vocoder's
//     unshifted 7-bit u7 to imbe_header_encode, which expects u7<<1, so
//     OP25-encoded frames lose the sync bit and carry a mis-shifted u7.
//     EncodeFEC does it correctly: pass Vectors as produced here.
//   - The reference's set_gain_adjust (d_gain_adjust) is always 0 in the
//     vocoder as used and is not ported.
package imbe
