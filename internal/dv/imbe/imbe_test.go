// SPDX-License-Identifier: GPL-3.0-or-later

package imbe

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
)

// The goldens in testdata/ were produced by testdata/harness.cc linked
// against the unmodified C++ reference; see testdata/README.md.

const (
	kFrames  = 300
	kSamples = kFrames * 160
	inSHA256 = "6f21ca48734297884247aace07b5b123a15ca2353bb42a42945d78e3d729acc5"
)

// sigGen mirrors gen_signal() in testdata/harness.cc exactly.
type lcg struct{ s uint32 }

func (l *lcg) next() uint32 {
	l.s = l.s*1103515245 + 12345
	return l.s
}

func isin(phase uint32) int32 {
	t := int32(phase) >> 16
	at := t
	if at < 0 {
		at = -at
	}
	y := int32((int64(4) * int64(t) * int64(32768-at)) >> 15)
	if y > 32767 {
		y = 32767
	}
	if y < -32767 {
		y = -32767
	}
	return y
}

func incFor(milliHz uint32) uint32 { return uint32((uint64(milliHz) << 32) / 8000000) }

func clip16(v int32) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

func genSignal() []int16 {
	out := make([]int16, kSamples)
	g := lcg{0x12345678}
	var ph [12]uint32
	for n := 0; n < kSamples; n++ {
		var s int32
		noise := int32((g.next()>>16)&0xFFFF) - 32768
		switch {
		case n < 12000:
			ph[0] += incFor(300000)
			ph[1] += incFor(1100000)
			ph[2] += incFor(2500000)
			s = (isin(ph[0])*6000 + isin(ph[1])*3000 + isin(ph[2])*1500) >> 15
			s += (noise * 800) >> 15
		case n < 16000:
			ph[0] += incFor(300000)
			ph[1] += incFor(1100000)
			ph[2] += incFor(2500000)
			s = (isin(ph[0])*30000 + isin(ph[1])*20000 + isin(ph[2])*10000) >> 15
			s += (noise * 4000) >> 15
		case n < 24000:
			s = 0
		case n < 40000:
			k := n - 24000
			mhz := 100000 + uint32((uint64(3400000)*uint64(k))/16000)
			ph[3] += incFor(mhz)
			s = (isin(ph[3]) * 9000) >> 15
		default:
			k := n - 40000
			if (k/1600)%2 == 0 {
				ph[4] += incFor(5000)
				f0 := 160000 + ((isin(ph[4]) * 60000) >> 15)
				for h := int32(1); h <= 7; h++ {
					ph[4+h] += incFor(uint32(f0 * h))
					s += (isin(ph[4+h]) * (7000 / h)) >> 15
				}
			} else {
				s = (noise * 5000) >> 15
			}
		}
		out[n] = clip16(s)
	}
	return out
}

func readGolden(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(name) == ".gz" {
		r, err := gzip.NewReader(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		if b, err = io.ReadAll(r); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

func TestSignalMatchesHarness(t *testing.T) {
	s := genSignal()
	buf := make([]byte, 2*len(s))
	for i, v := range s {
		binary.LittleEndian.PutUint16(buf[2*i:], uint16(v))
	}
	sum := sha256.Sum256(buf)
	if got := hex.EncodeToString(sum[:]); got != inSHA256 {
		t.Fatalf("signal sha256 = %s, want %s", got, inSHA256)
	}
}

func TestEncodeBitExact(t *testing.T) {
	want := readGolden(t, "enc.frames")
	if len(want) != kFrames*11 {
		t.Fatalf("golden has %d bytes", len(want))
	}
	sig := genSignal()
	e := NewEncoder()
	bad := 0
	for f := 0; f < kFrames; f++ {
		var pcm [160]int16
		copy(pcm[:], sig[f*160:])
		got := e.Encode(&pcm)
		if !bytes.Equal(got[:], want[f*11:f*11+11]) {
			if bad < 10 {
				var w [11]byte
				copy(w[:], want[f*11:])
				t.Errorf("frame %d: got %x (%v), want %x (%v)", f, got, UnpackVectors(&got), w, UnpackVectors(&w))
			}
			bad++
		}
	}
	if bad > 0 {
		t.Errorf("%d/%d frames differ", bad, kFrames)
	}
}

func testDecode(t *testing.T, framesName, pcmName string) {
	frames := readGolden(t, framesName)
	want := readGolden(t, pcmName)
	n := len(frames) / 11
	if len(want) != n*320 {
		t.Fatalf("golden sizes mismatch: %d frames, %d pcm bytes", n, len(want))
	}
	d := NewDecoder()
	bad := 0
	for f := 0; f < n; f++ {
		var fr [11]byte
		copy(fr[:], frames[f*11:])
		got := d.Decode(&fr)
		for i, v := range got {
			w := int16(binary.LittleEndian.Uint16(want[f*320+2*i:]))
			if v != w {
				if bad < 10 {
					t.Errorf("frame %d sample %d: got %d, want %d", f, i, v, w)
				}
				bad++
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d samples differ", bad)
	}
}

func TestDecodeBitExact(t *testing.T)       { testDecode(t, "enc.frames", "dec.pcm.gz") }
func TestDecodeRandomBitExact(t *testing.T) { testDecode(t, "rnd.frames", "rnddec.pcm.gz") }

func TestFECBitExact(t *testing.T) {
	recs := readGolden(t, "fec.bin.gz")
	const rl = 60
	if len(recs)%rl != 0 || len(recs) == 0 {
		t.Fatalf("bad fec golden size %d", len(recs))
	}
	for r := 0; r < len(recs)/rl; r++ {
		rec := recs[r*rl : (r+1)*rl]
		var fb [11]byte
		var cwWant, xb [18]byte
		copy(fb[:], rec[0:11])
		copy(cwWant[:], rec[11:29])
		copy(xb[:], rec[29:47])
		dbWant, e0Want, etWant := rec[47:58], int(rec[58]), int(rec[59])

		v := UnpackVectors(&fb)
		if p := PackVectors(v); p != fb {
			t.Fatalf("rec %d: pack/unpack round trip failed", r)
		}
		if cw := EncodeFEC(v); cw != cwWant {
			t.Errorf("rec %d: EncodeFEC = %x, want %x", r, cw, cwWant)
		}
		dv, e0, et := DecodeFEC(&xb)
		db := PackVectors(dv)
		if !bytes.Equal(db[:], dbWant) || e0 != e0Want || et != etWant {
			t.Errorf("rec %d: DecodeFEC = %x e0=%d et=%d, want %x e0=%d et=%d", r, db, e0, et, dbWant, e0Want, etWant)
		}
	}
}

func TestFECCorrectsUpToCapacity(t *testing.T) {
	g := lcg{7}
	for r := 0; r < 2000; r++ {
		var fb [11]byte
		for i := range fb {
			fb[i] = byte(g.next() >> 24)
		}
		v := UnpackVectors(&fb)
		cw := EncodeFEC(v)
		// up to 3 errors in the u0 Golay word, 1 in the u4 Hamming data bits
		for e := 0; e < 3; e++ {
			b := int(g.next()>>16) % 23
			cw[b/8] ^= 0x80 >> uint(b%8)
		}
		got, _, _ := DecodeFEC(&cw)
		if got[0] != v[0] {
			t.Fatalf("rec %d: u0 not corrected", r)
		}
	}
}

func TestDecodeArbitraryFramesNoPanic(t *testing.T) {
	g := lcg{0xDEADBEEF}
	d := NewDecoder()
	e := NewEncoder()
	for f := 0; f < 3000; f++ {
		var fr [11]byte
		for i := range fr {
			fr[i] = byte(g.next() >> 24)
		}
		pcm := d.Decode(&fr)
		e.Encode(&pcm)
	}
	// Extremes for the encoder.
	for _, v := range []int16{32767, -32768, 0} {
		var pcm [160]int16
		for i := range pcm {
			if i%2 == 0 {
				pcm[i] = v
			} else {
				pcm[i] = -v
			}
		}
		for k := 0; k < 10; k++ {
			e.Encode(&pcm)
		}
	}
}

func BenchmarkEncode(b *testing.B) {
	sig := genSignal()
	e := NewEncoder()
	var pcm [160]int16
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := i % kFrames
		copy(pcm[:], sig[f*160:])
		e.Encode(&pcm)
	}
}

func BenchmarkDecode(b *testing.B) {
	frames := readGolden(b, "enc.frames")
	d := NewDecoder()
	var fr [11]byte
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f := i % kFrames
		copy(fr[:], frames[f*11:])
		d.Decode(&fr)
	}
}
