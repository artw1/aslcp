package dmr

// Reed-Solomon (12,9) over GF(2^8) (primitive polynomial x^8+x^4+x^3+x^2+1,
// generator (x+a)(x+a^2)(x+a^3)), ETSI TS 102 361-1 B.3.6, as used for the
// full LC of the voice LC header and terminator.

var gfExp [512]byte
var gfLog [256]int

func init() {
	x := 1
	for i := 0; i < 255; i++ {
		gfExp[i] = byte(x)
		gfLog[x] = i
		x <<= 1
		if x&0x100 != 0 {
			x ^= 0x11D
		}
	}
	for i := 255; i < 512; i++ {
		gfExp[i] = gfExp[i-255]
	}
}

func gfMul(a, b byte) byte {
	if a == 0 || b == 0 {
		return 0
	}
	return gfExp[gfLog[a]+gfLog[b]]
}

func gfDiv(a, b byte) byte {
	if a == 0 {
		return 0
	}
	return gfExp[gfLog[a]-gfLog[b]+255]
}

// rsGen is the generator polynomial x^3 + 14x^2 + 56x + 64, low order first.
var rsGen = [3]byte{64, 56, 14}

// RS129 returns the three parity bytes (in transmission order) of 9 data
// bytes; the codeword is data followed by the parity.
func RS129(data [9]byte) (p [3]byte) {
	var r [3]byte // r[2] is the highest-order remainder coefficient
	for _, d := range data {
		f := d ^ r[2]
		r[2] = r[1] ^ gfMul(rsGen[2], f)
		r[1] = r[0] ^ gfMul(rsGen[1], f)
		r[0] = gfMul(rsGen[0], f)
	}
	return [3]byte{r[2], r[1], r[0]}
}

// RS129Correct checks a 12-byte codeword and corrects a single wrong byte in
// place. It reports whether the codeword is (now) valid.
func RS129Correct(cw *[12]byte) bool {
	var s [3]byte // s[j] = c(a^(j+1))
	for j := 0; j < 3; j++ {
		var v byte
		for i := 0; i < 12; i++ {
			v = gfMul(v, gfExp[j+1]) ^ cw[i]
		}
		s[j] = v
	}
	if s[0] == 0 && s[1] == 0 && s[2] == 0 {
		return true
	}
	if s[0] == 0 || s[1] == 0 {
		return false
	}
	x := gfDiv(s[1], s[0]) // a^n, n = 11 - position
	if gfMul(s[1], x) != s[2] {
		return false
	}
	n := gfLog[x]
	if n > 11 {
		return false
	}
	cw[11-n] ^= gfDiv(s[0], x)
	return true
}
