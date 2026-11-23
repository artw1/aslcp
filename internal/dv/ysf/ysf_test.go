// SPDX-License-Identifier: GPL-3.0-or-later

package ysf

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"os"
	"strconv"
	"strings"
	"testing"
)

type vec struct{ name, key, val string }

// loadVectors reads testdata/vectors.txt. The dch lines carry a 10-byte
// callsign with spaces, so they are cut by position.
func loadVectors(t *testing.T) []vec {
	t.Helper()
	b, err := os.ReadFile("testdata/vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	var vs []vec
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		l := sc.Text()
		if strings.HasPrefix(l, "dch ") {
			vs = append(vs, vec{"dch", l[4:14], l[15:]})
			continue
		}
		f := strings.Fields(l)
		switch len(f) {
		case 2:
			vs = append(vs, vec{f[0], "", f[1]})
		case 3:
			vs = append(vs, vec{f[0], f[1], f[2]})
		}
	}
	return vs
}

func unhex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newFrame() []byte {
	f := make([]byte, FrameLen)
	copy(f, Sync[:])
	return f
}

func TestVectors(t *testing.T) {
	seen := map[string]int{}
	for _, v := range loadVectors(t) {
		seen[v.name]++
		switch v.name {
		case "golay24128":
			d, _ := strconv.ParseUint(v.key, 16, 32)
			want, _ := strconv.ParseUint(v.val, 16, 32)
			if got := Golay24Encode(uint32(d)); got != uint32(want) {
				t.Errorf("golay %03x: %06x want %06x", d, got, want)
			}
			if got, ok := Golay24Decode(uint32(want) ^ 0x800402); !ok || got != uint32(d) {
				t.Errorf("golay decode %03x: %03x %v", d, got, ok)
			}
		case "crc":
			in := append(unhex(t, v.key), 0, 0)
			addCRC(in)
			if !bytes.Equal(in[len(in)-2:], unhex(t, v.val)) {
				t.Errorf("crc %x", in[len(in)-2:])
			}
		case "fich":
			var b [4]byte
			copy(b[:], unhex(t, v.key))
			fr := newFrame()
			FICHFromBytes(b).Encode(fr)
			if got := hex.EncodeToString(fr[5:30]); got != v.val {
				t.Errorf("fich %s:\n got %s\nwant %s", v.key, got, v.val)
			}
			if FICHFromBytes(b).Bytes() != b {
				t.Errorf("fich bytes roundtrip %s", v.key)
			}
			fr[7] ^= 0x40 // a channel bit error
			fr[20] ^= 0x01
			f, ok := DecodeFICH(fr)
			if !ok || f.Bytes() != b {
				t.Errorf("fich decode %s: %+v %v", v.key, f, ok)
			}
		case "dch":
			var dt [10]byte
			copy(dt[:], v.key)
			fr := newFrame()
			WriteDCH(fr, dt)
			var got []byte
			for i := 0; i < 5; i++ {
				got = append(got, fr[30+i*18:30+i*18+5]...)
			}
			if hex.EncodeToString(got) != v.val {
				t.Errorf("dch %q:\n got %x\nwant %s", v.key, got, v.val)
			}
			fr[31] ^= 0x10
			if d, ok := ReadDCH(fr); !ok || d != dt {
				t.Errorf("dch decode %q %v", d, ok)
			}
		case "csd":
			var c1, c2 [20]byte
			copy(c1[:], "ALL       N0CALL    ")
			copy(c2[:], strings.Repeat(" ", 20))
			fr := newFrame()
			WriteCSD(fr, c1, c2)
			if hex.EncodeToString(fr[30:]) != v.val {
				t.Errorf("csd:\n got %x\nwant %s", fr[30:], v.val)
			}
			fr[40] ^= 0x08
			d1, ok1, d2, ok2 := ReadCSD(fr)
			if !ok1 || !ok2 || d1 != c1 || d2 != c2 {
				t.Errorf("csd decode %q %v %q %v", d1, ok1, d2, ok2)
			}
		case "vch":
			var a [7]byte
			copy(a[:], unhex(t, v.key))
			want := unhex(t, v.val)
			for j := 0; j < 5; j++ {
				fr := newFrame()
				WriteVCH(fr, j, a)
				// The VCH sits de-interleaved in the frame; check it
				// against the reference's 13-byte interleaved form.
				var vch [13]byte
				off := vchOffset(j)
				for i := 0; i < 104; i++ {
					setBit(vch[:], i, getBit(fr[30:], off+i))
				}
				if !bytes.Equal(vch[:], want) {
					t.Fatalf("vch %x slot %d:\n got %x\nwant %x", a, j, vch, want)
				}
				setBit(fr[30:], off+5, !getBit(fr[30:], off+5)) // one repeated bit
				got, errs := ReadVCH(fr, j)
				if got != a || errs > 1 {
					t.Errorf("vch decode %x: %x errs %d", a, got, errs)
				}
			}
		}
	}
	for _, n := range []string{"golay24128", "crc", "fich", "dch", "csd", "vch"} {
		if seen[n] == 0 {
			t.Errorf("no %s vectors", n)
		}
	}
}

// TestVW checks the Voice FR codeword path against the reference: the
// 18-byte blocks are MMDVMHost CP25Audio::encode outputs for the imbe
// frames of the same index.
func TestVW(t *testing.T) {
	vs := loadVectors(t)
	frames := map[string][]byte{}
	for _, v := range vs {
		if strings.HasPrefix(v.name, "imbe") {
			frames[v.name[4:]] = unhex(t, v.val)
		}
	}
	n := 0
	for _, v := range vs {
		if !strings.HasPrefix(v.name, "vwblock") {
			continue
		}
		n++
		want := frames[v.name[7:]]
		fr := newFrame()
		copy(fr[30+2*18:], unhex(t, v.val))
		fr[30+2*18+3] ^= 0x20 // a bit error
		got, errs := ReadVW(fr, 2)
		if !bytes.Equal(got[:], want) || errs != 1 {
			t.Errorf("%s: %x want %x errs %d", v.name, got, want, errs)
		}
		var f11 [11]byte
		copy(f11[:], want)
		fr2 := newFrame()
		WriteVW(fr2, 2, f11)
		if hex.EncodeToString(fr2[30+36:30+54]) != v.val {
			t.Errorf("%s encode: %x", v.name, fr2[30+36:30+54])
		}
	}
	if n == 0 {
		t.Fatal("no vw vectors")
	}
}

func TestSilenceVCH(t *testing.T) {
	// MMDVM_CM YSF_SILENCE is the VCH of AMBESilence.
	fr := newFrame()
	WriteVCH(fr, 0, AMBESilence)
	var vch [13]byte
	for i := 0; i < 104; i++ {
		setBit(vch[:], i, getBit(fr[30:], 40+i))
	}
	if hex.EncodeToString(vch[:]) != "7bb28e4336e4a2397849336833" {
		t.Fatalf("%x", vch)
	}
}

// Errors are kept off the parity bit (bit 0): with it, decode24128 reports
// a 3-bit error as uncorrectable, and this port keeps that criterion.
func TestGolayCorrectsThree(t *testing.T) {
	for d := uint32(0); d < 4096; d += 37 {
		c := Golay24Encode(d)
		for _, e := range []uint32{1 << 3, 1<<1 | 1<<23, 1<<5 | 1<<9 | 1<<17} {
			if got, ok := Golay24Decode(c ^ e); !ok || got != d {
				t.Fatalf("d=%03x e=%06x got %03x %v", d, e, got, ok)
			}
		}
	}
}
