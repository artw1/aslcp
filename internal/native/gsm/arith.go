package gsm

import "math/bits"

// Fixed-point helpers from GSM 06.10 section 5.1. Values called "word" in the
// standard are int16 here and "longword" values are int32.

const (
	minWord = -32768
	maxWord = 32767
)

// sat clamps v to the int16 range.
func sat(v int32) int16 {
	if v < minWord {
		return minWord
	}
	if v > maxWord {
		return maxWord
	}
	return int16(v)
}

// add is the saturating 16-bit addition.
func add(a, b int16) int16 { return sat(int32(a) + int32(b)) }

// sub is the saturating 16-bit subtraction.
func sub(a, b int16) int16 { return sat(int32(a) - int32(b)) }

// mult is the Q15 multiplication with truncation.
func mult(a, b int16) int16 {
	if a == minWord && b == minWord {
		return maxWord
	}
	return int16((int32(a) * int32(b)) >> 15)
}

// multR is the Q15 multiplication with rounding.
func multR(a, b int16) int16 {
	if a == minWord && b == minWord {
		return maxWord
	}
	return int16((int32(a)*int32(b) + 16384) >> 15)
}

// abs16 is the saturating absolute value.
func abs16(a int16) int16 {
	if a == minWord {
		return maxWord
	}
	if a < 0 {
		return -a
	}
	return a
}

// lAdd is the saturating 32-bit addition.
func lAdd(a, b int32) int32 {
	s := int64(a) + int64(b)
	if s > 2147483647 {
		return 2147483647
	}
	if s < -2147483648 {
		return -2147483648
	}
	return int32(s)
}

// norm returns the number of left shifts that normalize a, that is make
// a<<norm(a) lie in [0x40000000, 0x7fffffff] for positive a or in
// [0x80000000, 0xc0000000] for negative a. a must not be zero.
func norm(a int32) int16 {
	if a < 0 {
		if a <= -1073741824 {
			return 0
		}
		a = ^a
	}
	return int16(bits.LeadingZeros32(uint32(a)) - 1)
}

// div returns num/denom in Q15 for 0 <= num <= denom, denom > 0.
func div(num, denom int16) int16 {
	if num == 0 {
		return 0
	}
	lNum := int32(num)
	lDenom := int32(denom)
	var q int16
	for k := 0; k < 15; k++ {
		q <<= 1
		lNum <<= 1
		if lNum >= lDenom {
			lNum -= lDenom
			q++
		}
	}
	return q
}

// asr is an arithmetic shift right by n that shifts left for negative n.
func asr(a int16, n int16) int16 {
	switch {
	case n >= 16:
		if a < 0 {
			return -1
		}
		return 0
	case n <= -16:
		return 0
	case n < 0:
		return a << uint(-n)
	default:
		return a >> uint(n)
	}
}

// asl is an arithmetic shift left by n that shifts right for negative n.
func asl(a int16, n int16) int16 {
	switch {
	case n >= 16:
		return 0
	case n <= -16:
		if a < 0 {
			return -1
		}
		return 0
	case n < 0:
		return asr(a, -n)
	default:
		return a << uint(n)
	}
}
