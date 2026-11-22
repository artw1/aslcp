package dmr

import (
	"math/rand"
	"testing"
)

// patterns calls f with every error pattern of weight 1..maxW over n bits.
func patterns(n, maxW int, f func(e uint32)) {
	var rec func(start, w int, e uint32)
	rec = func(start, w int, e uint32) {
		if w > 0 {
			f(e)
		}
		if w == maxW {
			return
		}
		for i := start; i < n; i++ {
			rec(i+1, w+1, e|1<<i)
		}
	}
	rec(0, 0, 0)
}

func TestGolayKnown(t *testing.T) {
	// Codewords of data 1 (the generator), from ETSI TS 102 361-1 B.3.1/B.3.2
	// and MMDVMHost's encoding tables.
	if c := golay23Encode(1); c != 0xC75 {
		t.Errorf("golay23(1) = %x", c)
	}
	if c := golay24Encode(1); c != 0x18EB {
		t.Errorf("golay24(1) = %x", c)
	}
	if c := golay20Encode(1); c != 0x18EB {
		t.Errorf("golay20(1) = %x", c)
	}
	if c := qrEncode(1); c != 0x0273 {
		t.Errorf("qr(1) = %x", c)
	}
	if c := qrEncode(0x7F); c != 0xFE5B {
		t.Errorf("qr(7f) = %x", c)
	}
}

func TestGolay24Errors(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for n := 0; n < 300; n++ {
		d := uint32(r.Intn(4096))
		c := golay24Encode(d)
		patterns(24, 3, func(e uint32) {
			got, errs, ok := golay24Decode(c ^ e)
			if !ok || got != d || errs != popcount(e) {
				t.Fatalf("golay24 %03x e=%06x: %03x %d %v", d, e, got, errs, ok)
			}
		})
		// Every 4-bit error is detected.
		for k := 0; k < 50; k++ {
			e := uint32(0)
			for popcount(e) < 4 {
				e |= 1 << r.Intn(24)
			}
			if _, _, ok := golay24Decode(c ^ e); ok {
				t.Fatalf("4 errors %06x not detected", e)
			}
		}
	}
}

func TestGolay23Errors(t *testing.T) {
	for _, d := range []uint32{0, 1, 0x800, 0xFFF, 0xA5A, 0x123} {
		c := golay23Encode(d)
		patterns(23, 3, func(e uint32) {
			if got, errs := golay23Decode(c ^ e); got != d || errs != popcount(e) {
				t.Fatalf("golay23 %03x e=%06x: %03x %d", d, e, got, errs)
			}
		})
	}
}

func TestGolay20Errors(t *testing.T) {
	for d := 0; d < 256; d++ {
		c := golay20Encode(uint8(d))
		patterns(20, 3, func(e uint32) {
			if got, errs, ok := golay20Decode(c ^ e); !ok || int(got) != d || errs != popcount(e) {
				t.Fatalf("golay20 %02x e=%05x: %02x %d %v", d, e, got, errs, ok)
			}
		})
	}
}

func TestQRErrors(t *testing.T) {
	for d := 0; d < 128; d++ {
		c := qrEncode(uint8(d))
		if got, errs, ok := qrDecode(c); !ok || int(got) != d || errs != 0 {
			t.Fatalf("qr %02x clean", d)
		}
		patterns(16, 2, func(e uint32) {
			if got, _, ok := qrDecode(c ^ uint16(e)); !ok || int(got) != d {
				t.Fatalf("qr %02x e=%04x: %02x %v", d, e, got, ok)
			}
		})
	}
}

func TestQRMinDistance(t *testing.T) {
	for i := range qr16Table {
		for j := i + 1; j < len(qr16Table); j++ {
			if popcount(uint32(qr16Table[i]^qr16Table[j])) < 6 {
				t.Fatalf("QR distance < 6 between %d and %d", i, j)
			}
		}
	}
}

func TestHammingSingle(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for _, h := range []*hamming{hamming15113, hamming1393, hamming16114} {
		for n := 0; n < 50; n++ {
			d := make([]bool, h.n)
			for i := 0; i < h.k; i++ {
				d[i] = r.Intn(2) == 1
			}
			h.encode(d)
			if fixed, ok := h.decode(append([]bool(nil), d...)); fixed || !ok {
				t.Fatal("clean word changed")
			}
			for i := 0; i < h.n; i++ {
				x := append([]bool(nil), d...)
				x[i] = !x[i]
				if fixed, ok := h.decode(x); !fixed || !ok {
					t.Fatalf("(%d,%d) bit %d not fixed", h.n, h.k, i)
				}
				for j := range x {
					if x[j] != d[j] {
						t.Fatalf("(%d,%d) bit %d: wrong fix", h.n, h.k, i)
					}
				}
			}
		}
	}
}

func TestRS129(t *testing.T) {
	// The ETSI generator polynomial: (x+a)(x+a^2)(x+a^3) = x^3+14x^2+56x+64.
	r := rand.New(rand.NewSource(3))
	for n := 0; n < 200; n++ {
		var d [9]byte
		r.Read(d[:])
		p := RS129(d)
		var cw [12]byte
		copy(cw[:], d[:])
		copy(cw[9:], p[:])
		c := cw
		if !RS129Correct(&c) || c != cw {
			t.Fatal("clean codeword rejected")
		}
		for pos := 0; pos < 12; pos++ {
			c := cw
			c[pos] ^= byte(1 + r.Intn(255))
			if !RS129Correct(&c) || c != cw {
				t.Fatalf("single byte error at %d not corrected", pos)
			}
		}
	}
}

func randAMBE(r *rand.Rand) AMBE {
	var f AMBE
	r.Read(f[:])
	f[6] &= 0x80
	return f
}

func TestAMBERoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for n := 0; n < 2000; n++ {
		f := randAMBE(r)
		got, errs := DecodeAMBE(EncodeAMBE(f))
		if got != f || errs != 0 {
			t.Fatalf("round trip %x -> %x (%d)", f, got, errs)
		}
	}
}

// ambeFlip flips C bits (frame x, bit j) in a 72-bit frame.
func ambeFlip(d AMBE72, x, j int) AMBE72 {
	for i := 0; i < 36; i++ {
		if int(ambeRW[i]) == x && int(ambeRX[i]) == j {
			setBit(d[:], 2*i, !getBit(d[:], 2*i))
		}
		if int(ambeRY[i]) == x && int(ambeRZ[i]) == j {
			setBit(d[:], 2*i+1, !getBit(d[:], 2*i+1))
		}
	}
	return d
}

func TestAMBEErrors(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for n := 0; n < 3000; n++ {
		f := randAMBE(r)
		d := EncodeAMBE(f)
		ea, eb := r.Intn(4), r.Intn(4)
		for _, j := range r.Perm(24)[:ea] {
			d = ambeFlip(d, 0, j)
		}
		for _, j := range r.Perm(23)[:eb] {
			d = ambeFlip(d, 1, j)
		}
		got, errs := DecodeAMBE(d)
		if ea >= 2 && ea+eb >= 6 {
			if got != SilenceAMBE {
				t.Fatalf("ea=%d eb=%d: not muted", ea, eb)
			}
			continue
		}
		if got != f || errs != ea+eb {
			t.Fatalf("ea=%d eb=%d: %x -> %x (%d)", ea, eb, f, got, errs)
		}
	}
	// Four errors in C0 mute the frame.
	d := EncodeAMBE(randAMBE(r))
	for _, j := range []int{0, 5, 11, 20} {
		d = ambeFlip(d, 0, j)
	}
	if got, errs := DecodeAMBE(d); got != SilenceAMBE || errs != 10 {
		t.Fatalf("4 errors in C0: %x %d", got, errs)
	}
}

func TestSilence(t *testing.T) {
	if EncodeAMBE(SilenceAMBE) != SilenceAMBE72 {
		t.Fatalf("EncodeAMBE(SilenceAMBE) = %x", EncodeAMBE(SilenceAMBE))
	}
	if f, errs := DecodeAMBE(SilenceAMBE72); f != SilenceAMBE || errs != 0 {
		t.Fatalf("DecodeAMBE(silence72) = %x %d", f, errs)
	}
	// The 7-byte prefix of the 72-bit frame is not a 49-bit silence frame.
	if EncodeAMBE(AMBE{0xb9, 0xe8, 0x81, 0x52, 0x61, 0x73, 0x00}) == SilenceAMBE72 {
		t.Fatal("unexpected")
	}
}

func TestBPTCErrors(t *testing.T) {
	r := rand.New(rand.NewSource(6))
	for n := 0; n < 50; n++ {
		var info [12]byte
		r.Read(info[:])
		var b Burst
		b.PutBPTC196(info)
		if got, fixed, ok := b.BPTC196(); got != info || fixed != 0 || !ok {
			t.Fatal("clean BPTC")
		}
		for j := 0; j < 196; j++ {
			x := b
			p := bptcPos(j)
			setBit(x[:], p, !getBit(x[:], p))
			got, _, ok := x.BPTC196()
			if j != 0 && (got != info || !ok) { // bit 0 of the matrix (R3) is unprotected and unused
				t.Fatalf("BPTC bit %d not corrected", j)
			}
		}
		// Up to three errors in distinct rows and columns are corrected.
		x := b
		for k, rc := range r.Perm(9)[:3] {
			m := 1 + rc*15 + (k*5+rc)%15 // matrix index in row rc, distinct columns
			p := bptcPos((m * 181) % 196)
			setBit(x[:], p, !getBit(x[:], p))
		}
		if got, _, ok := x.BPTC196(); got != info || !ok {
			t.Fatalf("3 spread errors not corrected")
		}
	}
}

func TestEmbeddedLCErrors(t *testing.T) {
	lc := LC{FLCO: FLCOGroup, Dst: 1997, Src: 1234567}
	frags := EmbeddedLC(lc)
	for i := 0; i < 128; i++ {
		x := frags
		setBit(x[i/32][:], i%32, !getBit(x[i/32][:], i%32))
		if got, ok := DecodeEmbeddedLC(x); embOrder[i] < 112 && (!ok || got != lc) {
			// Bits in the column-parity row cannot be corrected, only detected.
			t.Fatalf("embedded bit %d: %+v %v", i, got, ok)
		}
	}
	// Two errors in one row are detected.
	x := frags
	for _, m := range []int{0, 1} {
		var a int
		for a = 0; embOrder[a] != m; a++ {
		}
		setBit(x[a/32][:], a%32, !getBit(x[a/32][:], a%32))
	}
	if _, ok := DecodeEmbeddedLC(x); ok {
		t.Fatal("double error accepted")
	}
}
