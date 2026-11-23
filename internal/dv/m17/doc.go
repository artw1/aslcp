// SPDX-License-Identifier: GPL-3.0-or-later

// Package m17 is the protocol layer of the M17 digital voice mode: address
// (callsign) base-40 encoding, the CRC-16, the Link Setup Frame (LSF) and
// TYPE field, the 54-byte M17-over-IP stream packet, the reflector control
// packets (CONN, LSTN, ACKN, NACK, PING, PONG, DISC) and the "M17-XXX C"
// tune target. It does no network I/O and has no dependencies beyond the
// standard library.
//
// # Sources
//
//   - M17 specification, Part I (air interface), version 2.1.1 as served by
//     https://spec.m17project.org (LaTeX source
//     https://raw.githubusercontent.com/M17-Project/M17_spec/master/M17_spec.tex,
//     read 2026-11-23): Appendix "Address Encoding" (base-40 alphabet and
//     the reserved / standard / extended / broadcast ranges), the CRC
//     section (polynomial 0x5935, initial value 0xFFFF, test vectors), and
//     the TYPE field tables.
//   - M17 Internet Interface (M17_inet, status "Draft"):
//     https://github.com/M17-Project/M17_inet, file "M17 Internet
//     Interface.md" on the main branch, read 2026-11-23: stream packet and
//     reflector control packets. The commit hash could not be looked up
//     from the build environment (GitHub API access was not available).
//   - Cross-check of the client-side control packet sizes and offsets
//     against mrefd protocol.cpp (https://github.com/n7tae/mrefd, master,
//     read 2026-11-23, commit hash not looked up): CONN/LSTN 11 bytes,
//     PING/PONG/DISC 10 bytes, ACKN/NACK and the DISC acknowledgement
//     4 bytes.
//
// # Layouts (all multi-byte fields big endian)
//
//	stream packet, 54 bytes
//	   0..3   "M17 "
//	   4..5   StreamID
//	   6..33  LSD: DST 6, SRC 6, TYPE 2, META 14
//	  34..35  FN, bit 15 = last frame, bits 14..0 counter
//	  36..51  payload, two Codec2 3200 frames of 8 bytes (40 ms)
//	  52..53  CRC-16 over bytes 0..51
//
//	CONN / LSTN, 11 bytes   magic, callsign address 6, module 'A'..'Z'
//	PING / PONG / DISC, 10  magic, callsign address 6
//	ACKN / NACK / DISC, 4   magic only (server replies; DISC form is the
//	                        acknowledgement of a client DISC)
//
// The over-the-air LSF is the 28 LSD bytes followed by a CRC-16 over them
// (30 bytes); LSF.MarshalCRC and UnmarshalLSFCRC handle that form.
//
// # TYPE field
//
// Bit 0 packet(0)/stream(1), bits 2..1 data type (1 data, 2 voice, 3
// voice+data), bits 4..3 encryption type, bits 6..5 encryption subtype
// (META content), bits 10..7 CAN, bit 11 signed stream, bits 15..12
// reserved. Plain Codec2 3200 voice with no encryption is 0x0005 | CAN<<7.
// The spec presents this as a table of byte 0 / byte 1 bit fields; the
// 16-bit form above is the same layout read as a big endian word.
//
// # Not verified without a live reflector
//
// The package is checked against the spec test vectors and hand-built
// golden packets only. Not verified against live traffic: the TYPE/CAN
// placement as emitted by real clients, whether a given reflector accepts a
// callsign or DST form, the 37-byte reflector-to-reflector CONN/ACKN
// (interlink) packets (not implemented, they are rejected), and the
// signed-stream authentication frames (not interpreted).
package m17
