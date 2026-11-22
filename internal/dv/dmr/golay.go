package dmr

// Golay(23,12,7), Golay(24,12,8), Golay(20,8,7) and QR(16,7,6).
//
// The codes are built from their generator polynomials (ETSI TS 102 361-1
// annex B); the systematic form (data in the high bits, parity in the low bits,
// extended codes with an even overall parity bit last) matches the encoding
// tables of MMDVMHost (Golay24128.cpp, Golay2087.cpp, QR1676.cpp, G4KLX,
// GPL-2.0-or-later), which the tests check against vectors from that code.

const (
	golayPoly = 0xC75 // x^11+x^10+x^6+x^5+x^4+x^2+1
	qrPoly    = 0x139 // x^8+x^5+x^4+x^3+1, (15,7) cyclic code
)

// polyRem returns v mod g over GF(2); deg is the degree of g.
func polyRem(v uint32, g uint32, deg int) uint32 {
	for i := 31; i >= deg; i-- {
		if v&(1<<i) != 0 {
			v ^= g << (i - deg)
		}
	}
	return v
}

// golay23Syn maps a Golay(23,12) syndrome to its error pattern (weight <= 3);
// the code is perfect, so every syndrome has one.
var golay23Syn [2048]uint32

func init() {
	golay23Syn[0] = 0
	for i := 0; i < 23; i++ {
		ei := uint32(1) << i
		golay23Syn[polyRem(ei, golayPoly, 11)] = ei
		for j := i + 1; j < 23; j++ {
			ej := ei | 1<<j
			golay23Syn[polyRem(ej, golayPoly, 11)] = ej
			for k := j + 1; k < 23; k++ {
				ek := ej | 1<<k
				golay23Syn[polyRem(ek, golayPoly, 11)] = ek
			}
		}
	}
}

// golay23Encode returns the 23-bit codeword of 12 data bits.
func golay23Encode(d uint32) uint32 {
	d &= 0xFFF
	return d<<11 | polyRem(d<<11, golayPoly, 11)
}

// golay23Decode corrects up to 3 errors and returns the data and the number
// of corrected bits.
func golay23Decode(c uint32) (d uint32, errs int) {
	c &= 0x7FFFFF
	e := golay23Syn[polyRem(c, golayPoly, 11)]
	return (c ^ e) >> 11, popcount(e)
}

// golay24Encode returns the 24-bit extended Golay codeword of 12 data bits.
func golay24Encode(d uint32) uint32 {
	c := golay23Encode(d)
	return c<<1 | uint32(popcount(c)&1)
}

// golay24Decode corrects up to 3 errors; ok is false when 4 errors are
// detected.
func golay24Decode(c uint32) (d uint32, errs int, ok bool) {
	c &= 0xFFFFFF
	c23 := c >> 1
	e := golay23Syn[polyRem(c23, golayPoly, 11)]
	fixed := c23 ^ e
	errs = popcount(e)
	if uint32(popcount(fixed)&1) != c&1 {
		errs++
	}
	if errs > 3 {
		return fixed >> 11, errs, false
	}
	return fixed >> 11, errs, true
}

// golay20Encode returns the 20-bit Golay(20,8,7) codeword of 8 data bits (the
// slot type): a shortened Golay(23,12) codeword plus an even parity bit.
func golay20Encode(d uint8) uint32 {
	c := golay23Encode(uint32(d)) // 19 significant bits
	return c<<1 | uint32(popcount(c)&1)
}

var golay20Table [256]uint32
var qr16Table [128]uint16

func init() {
	for i := range golay20Table {
		golay20Table[i] = golay20Encode(uint8(i))
	}
	for i := range qr16Table {
		qr16Table[i] = qrEncode(uint8(i))
	}
}

// golay20Decode returns the nearest codeword's data if it is within 3 bits.
func golay20Decode(c uint32) (d uint8, errs int, ok bool) {
	best, bd := 0, 99
	for i, w := range golay20Table {
		if n := popcount(w ^ (c & 0xFFFFF)); n < bd {
			best, bd = i, n
		}
	}
	return uint8(best), bd, bd <= 3
}

// qrEncode returns the QR(16,7,6) codeword of 7 data bits: a (15,7) cyclic
// codeword plus an even parity bit.
func qrEncode(d uint8) uint16 {
	v := uint32(d&0x7F) << 8
	c := v | polyRem(v, qrPoly, 8)
	return uint16(c<<1 | uint32(popcount(c)&1))
}

// qrDecode returns the nearest codeword's data if it is within 2 bits.
func qrDecode(c uint16) (d uint8, errs int, ok bool) {
	best, bd := 0, 99
	for i, w := range qr16Table {
		if n := popcount(uint32(w ^ c)); n < bd {
			best, bd = i, n
		}
	}
	return uint8(best), bd, bd <= 2
}
