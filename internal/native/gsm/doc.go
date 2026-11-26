// Package gsm implements the GSM 06.10 full-rate speech codec (RPE-LTP).
//
// The encoder and decoder follow the fixed-point arithmetic of ETSI
// EN 300 961 (GSM 06.10) step by step, so their output is bit-exact with the
// reference implementation. One frame is 160 samples of signed 16-bit linear
// PCM at 8000 Hz (20 ms) and packs into 33 bytes, using the layout of RTP
// payload type 3 (RFC 3551) that EchoLink also uses: a 0xD signature nibble
// followed by the 76 parameters, most significant bit first.
//
// Encoder and Decoder keep state between frames and are not safe for
// concurrent use; give each stream its own.
package gsm
