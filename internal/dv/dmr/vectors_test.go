package dmr

import (
	"bufio"
	"encoding/hex"
	"os"
	"strings"
	"testing"
)

// vectors returns the lines of testdata/vectors.txt (made by
// testdata/harness.cc against MMDVMHost) whose first field is kind.
func vectors(t *testing.T, kind string) [][]string {
	t.Helper()
	f, err := os.Open("testdata/vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out [][]string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fs := strings.Fields(sc.Text())
		if len(fs) > 0 && fs[0] == kind {
			out = append(out, fs[1:])
		}
	}
	if len(out) == 0 {
		t.Fatalf("no %s vectors", kind)
	}
	return out
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestAMBEVectors(t *testing.T) {
	for _, v := range vectors(t, "ambe") {
		var u AMBE
		var d AMBE72
		copy(u[:], unhex(t, v[0]))
		copy(d[:], unhex(t, v[1]))
		if got := EncodeAMBE(u); got != d {
			t.Errorf("EncodeAMBE(%x) = %x, want %x", u, got, d)
		}
		got, errs := DecodeAMBE(d)
		if got != u || errs != 0 {
			t.Errorf("DecodeAMBE(%x) = %x/%d, want %x", d, got, errs, u)
		}
		a := getBits(u[:], 0, 12)
		var p uint32
		for _, c := range v[2] {
			p = p<<4 | uint32(strings.IndexRune("0123456789abcdef", c))
		}
		if ambePRNG(a) != p {
			t.Errorf("PRNG(%03x) = %06x, want %06x", a, ambePRNG(a), p)
		}
	}
}

func TestEMBVectors(t *testing.T) {
	for _, v := range vectors(t, "emb") {
		e := EMB{CC: uint8(atoi(t, v[0])), PI: v[1] == "1", LCSS: LCSS(atoi(t, v[2]))}
		var want Burst
		copy(want[13:20], unhex(t, v[3]))
		var b Burst
		b.PutEMB(e)
		if b != want {
			t.Errorf("EMB %+v: got %x want %x", e, b[13:20], want[13:20])
		}
		if got, ok := want.EMB(); !ok || got != e {
			t.Errorf("decode EMB %x = %+v %v", want[13:20], got, ok)
		}
	}
}

func TestSlotTypeVectors(t *testing.T) {
	for _, v := range vectors(t, "slottype") {
		st := SlotType{CC: uint8(atoi(t, v[0])), DataType: DataType(atoi(t, v[1]))}
		var want Burst
		copy(want[12:21], unhex(t, v[2]))
		var b Burst
		b.PutSlotType(st)
		if b != want {
			t.Errorf("slot type %+v: got %x want %x", st, b[12:21], want[12:21])
		}
		if got, ok := want.SlotType(); !ok || got != st {
			t.Errorf("decode slot type = %+v %v", got, ok)
		}
	}
}

// bptcOnly clears everything but the BPTC bits.
func bptcOnly(b Burst) Burst {
	var o Burst
	for j := 0; j < 196; j++ {
		setBit(o[:], bptcPos(j), getBit(b[:], bptcPos(j)))
	}
	return o
}

func TestFullLCVectors(t *testing.T) {
	for _, v := range vectors(t, "lc") {
		lc, err := ParseLC(unhex(t, v[0]))
		if err != nil {
			t.Fatal(err)
		}
		for i, dt := range []DataType{DataVoiceLCHeader, DataTerminatorWithLC} {
			var want Burst
			copy(want[:], unhex(t, v[1+i]))
			b := LCBurst(lc, dt, 1)
			if got := bptcOnly(b); got != want {
				t.Errorf("LC %x dt %d:\n got %x\nwant %x", v[0], dt, got, want)
			}
			got, st, err := ParseLCBurst(&b)
			if err != nil || got != lc || st.DataType != dt || st.CC != 1 {
				t.Errorf("ParseLCBurst = %+v %+v %v", got, st, err)
			}
			if got, err := DecodeLCBurst(&want, dt); err != nil || got != lc {
				t.Errorf("DecodeLCBurst(MMDVMHost) = %+v %v", got, err)
			}
		}
	}
}

func TestEmbeddedLCVectors(t *testing.T) {
	for _, v := range vectors(t, "emblc") {
		lc, _ := ParseLC(unhex(t, v[0]))
		frags := EmbeddedLC(lc)
		var want [4][4]byte
		for k := 0; k < 4; k++ {
			// "lcss:5 bytes of burst[14:19]"
			var b Burst
			copy(b[14:19], unhex(t, v[1+k][2:]))
			want[k] = b.Embedded()
			if lcss := []string{"1", "3", "3", "2"}[k]; v[1+k][:1] != lcss {
				t.Errorf("fragment %d LCSS %s", k, v[1+k][:1])
			}
		}
		if frags != want {
			t.Errorf("EmbeddedLC(%x) = %x want %x", v[0], frags, want)
		}
		if got, ok := DecodeEmbeddedLC(want); !ok || got != lc {
			t.Errorf("DecodeEmbeddedLC = %+v %v", got, ok)
		}
	}
}

// TestRegenerateVectors compares DecodeAMBE+EncodeAMBE with MMDVMHost's
// regenerateDMR on bursts with 0-5 bit errors per frame and on random bursts.
func TestRegenerateVectors(t *testing.T) {
	mmdvmSub := AMBE72{0x8e, 0xa8, 0x42, 0x00, 0x04, 0x44, 0x00, 0x80, 0x84}
	for _, v := range vectors(t, "regen") {
		var in, want Burst
		copy(in[:], unhex(t, v[0]))
		copy(want[:], unhex(t, v[1]))
		wantErrs := atoi(t, v[2])
		fin, fwant := in.Voice72(), want.Voice72()
		total := 0
		exact := true
		for k := 0; k < 3; k++ {
			f, errs := DecodeAMBE(fin[k])
			total += errs
			if fwant[k] == mmdvmSub {
				// MMDVMHost substituted the frame. It also rejects some
				// correctable C0 words (2 data errors + parity error), which
				// we correct, so only the error total is not comparable.
				exact = false
				continue
			}
			if got := EncodeAMBE(f); got != fwant[k] {
				t.Errorf("regenerate %x = %x, want %x", fin[k], got, fwant[k])
			}
		}
		if exact && total != wantErrs {
			t.Errorf("errors %d, want %d", total, wantErrs)
		}
	}
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			t.Fatalf("bad number %q", s)
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// TestPRNGTable checks the generated whitening against MMDVMHost's 4096-entry
// PRNG_TABLE (whose entries are our 23-bit word shifted left by one).
func TestPRNGTable(t *testing.T) {
	h := uint32(2166136261)
	for a := uint32(0); a < 4096; a++ {
		v := ambePRNG(a)
		for s := 16; s >= 0; s -= 8 {
			h ^= (v >> s) & 0xFF
			h *= 16777619
		}
	}
	want := vectors(t, "prngfnv")[0][0]
	if got := hex.EncodeToString([]byte{byte(h >> 24), byte(h >> 16), byte(h >> 8), byte(h)}); got != want {
		t.Errorf("PRNG table FNV %s, want %s", got, want)
	}
}
