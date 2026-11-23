package nxdn

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
)

// vectors are produced by the MMDVMHost C++ classes (see testdata/README.md).
func vectors(t *testing.T) map[string]string {
	t.Helper()
	f, err := os.Open("testdata/mmdvmhost_vectors.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), " ")
		if ok {
			m[k] = v
		}
	}
	return m
}

func hx(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return b
}

func TestCRCVectors(t *testing.T) {
	v := vectors(t)
	a := hx(t, "01002004d207cd000000")
	a = append(a, 0, 0)
	EncodeCRC12(a, 80)
	if got := hex.EncodeToString(a); got != v["crc12.vcall"] {
		t.Errorf("crc12 %s want %s", got, v["crc12.vcall"])
	}
	if !CheckCRC12(a, 80) {
		t.Error("crc12 check")
	}
	b := []byte{0xC5, 0x10, 0, 0}
	EncodeCRC6(b, 26)
	if got := hex.EncodeToString(b); got != v["crc6.sacch"] {
		t.Errorf("crc6 %s want %s", got, v["crc6.sacch"])
	}
	if !CheckCRC6(b, 26) {
		t.Error("crc6 check")
	}
	c := []byte{0x12, 0x34, 0x56, 0, 0, 0}
	EncodeCRC15(c, 24)
	if got := hex.EncodeToString(c); got != v["crc15.synth"] {
		t.Errorf("crc15 %s want %s", got, v["crc15.synth"])
	}
	if !CheckCRC15(c, 24) {
		t.Error("crc15 check")
	}
	c[0] ^= 1
	if CheckCRC15(c, 24) {
		t.Error("crc15 accepted corruption")
	}
}

func TestConvVectors(t *testing.T) {
	v := vectors(t)
	got := ConvEncode(hx(t, v["conv.36"]), 36)
	// the C++ output buffer is 9 bytes; only 72 bits are defined
	if hex.EncodeToString(got) != v["conv.36.out"][:18] {
		t.Errorf("conv36 %x want %s", got, v["conv.36.out"])
	}
	got = ConvEncode(hx(t, v["conv.96"]), 96)
	if hex.EncodeToString(got) != v["conv.96.out"] {
		t.Errorf("conv96 %x want %s", got, v["conv.96.out"])
	}
}

func TestLICHVectors(t *testing.T) {
	v := vectors(t)
	p := []struct{ rfct, usc, opt, dir int }{
		{2, 0, 0, 0}, {2, 0, 0, 1}, {2, 2, 3, 0}, {2, 2, 3, 1},
		{2, 2, 1, 0}, {2, 2, 2, 1}, {1, 3, 0, 0}, {0, 1, 2, 1},
	}
	for i, c := range p {
		l := NewLICH(c.rfct, c.usc, c.opt, c.dir)
		if got := fmt.Sprintf("%02x", byte(l)); got != v[fmt.Sprintf("lich.%d.raw", i)] {
			t.Errorf("lich %d raw %s want %s", i, got, v[fmt.Sprintf("lich.%d.raw", i)])
		}
		frame := make([]byte, FrameBytes)
		l.Encode(frame)
		if got := hex.EncodeToString(frame); got != v[fmt.Sprintf("lich.%d.frame", i)] {
			t.Errorf("lich %d frame %s want %s", i, got, v[fmt.Sprintf("lich.%d.frame", i)])
		}
		d, ok := DecodeLICH(frame)
		if !ok || d != l || d.RFCT() != c.rfct || d.USC() != c.usc || d.Option() != c.opt || d.Direction() != c.dir {
			t.Errorf("lich %d decode %02x ok=%v", i, byte(d), ok)
		}
	}
	if LICHHeader != 0x81 || LICHVoice != 0xAC {
		t.Errorf("LICH constants %02x %02x", byte(LICHHeader), byte(LICHVoice))
	}
	if (LICH(0x80)).ParityOK() {
		t.Error("0x80 should fail parity")
	}
}

func TestSACCHVectors(t *testing.T) {
	v := vectors(t)
	datas := [][3]byte{{0x10, 0, 0}, {0x01, 0x00, 0x20}, {0x04, 0xD2, 0x07}}
	structs := []int{0, 3, 2, 1}
	rans := []int{0, 1, 63, 17}
	k := 0
	for s := 0; s < 4; s++ {
		for d := 0; d < 3; d++ {
			sc := NewSACCH(structs[s], rans[(s+d)&3], datas[d])
			raw := sc.Raw()
			key := fmt.Sprintf("sacch.%d", k)
			if got := hex.EncodeToString(raw[:]); got != v[key+".raw"] {
				t.Errorf("%s raw %s want %s", key, got, v[key+".raw"])
			}
			frame := make([]byte, FrameBytes)
			sc.Encode(frame)
			if got := hex.EncodeToString(frame); got != v[key+".frame"] {
				t.Errorf("%s frame %s want %s", key, got, v[key+".frame"])
			}
			dec, ok := DecodeSACCH(frame)
			dr := dec.Raw()
			if !ok || hex.EncodeToString(dr[:]) != v[key+".raw"] {
				t.Errorf("%s decode ok=%v %x", key, ok, dr)
			}
			if dec.RAN() != rans[(s+d)&3] || dec.Structure() != structs[s] || dec.Data() != func() [3]byte { x := datas[d]; x[2] &= 0xC0; return x }() {
				t.Errorf("%s fields %d %d %x", key, dec.RAN(), dec.Structure(), dec.Data())
			}
			k++
		}
	}
}

func TestFACCH1Vectors(t *testing.T) {
	v := vectors(t)
	msgs := [][10]byte{
		{0x01, 0x00, 0x20, 0x04, 0xD2, 0x07, 0xCD, 0, 0, 0},
		{0x08, 0x00, 0x20, 0x04, 0xD2, 0x07, 0xCD, 0, 0, 0},
		{0x01, 0x00, 0x80, 0xFF, 0xFE, 0x00, 0x01, 0, 0, 0},
		{0xDE, 0xAD, 0xBE, 0xEF, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06},
	}
	for i, m := range msgs {
		key := fmt.Sprintf("facch1.%d", i)
		raw := FACCH1Raw(m)
		if got := hex.EncodeToString(raw[:]); got != v[key+".raw"] {
			t.Errorf("%s raw %s want %s", key, got, v[key+".raw"])
		}
		f1 := make([]byte, FrameBytes)
		EncodeFACCH1(f1, FACCH1Offset1, m)
		if got := hex.EncodeToString(f1); got != v[key+".frame1"] {
			t.Errorf("%s frame1 %s want %s", key, got, v[key+".frame1"])
		}
		f2 := make([]byte, FrameBytes)
		EncodeFACCH1(f2, FACCH1Offset2, m)
		if got := hex.EncodeToString(f2); got != v[key+".frame2"] {
			t.Errorf("%s frame2 %s want %s", key, got, v[key+".frame2"])
		}
		d, ok := DecodeFACCH1(f1, FACCH1Offset1)
		if !ok || d != m {
			t.Errorf("%s decode ok=%v %x", key, ok, d)
		}
		// The error-injected frame decodes exactly as the C++ decoder does.
		bad := hx(t, v[key+".bad"])
		d, ok = DecodeFACCH1(bad, FACCH1Offset1)
		want := fmt.Sprintf("ok=%d %x", b2i(ok), d)
		if cpp := v[key+".baddecode"]; want != cpp {
			t.Errorf("%s baddecode %q, MMDVMHost gives %q", key, want, cpp)
		}
	}
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

func TestLayer3Vector(t *testing.T) {
	l := NewVCall(1234, 1997, true)
	if want := hx(t, "01002004d207cd000000"); !bytes.Equal(l[:], want) {
		t.Errorf("vcall %x want %x", l, want)
	}
	if l.Type() != MsgVCall || l.Src() != 1234 || l.Dst() != 1997 || !l.IsGroup() {
		t.Errorf("accessors %d %d %d %v", l.Type(), l.Src(), l.Dst(), l.IsGroup())
	}
	p := NewVCall(1234, 1997, false)
	if p.IsGroup() || p[2] != 0x80 {
		t.Errorf("private %x", p)
	}
	if l.TxRel()[0] != MsgTxRel || l.TxRel().Src() != 1234 {
		t.Error("txrel")
	}
	// superframe parts reassemble to the first 72 bits
	var re Layer3
	for i := 0; i < 4; i++ {
		re.SetSACCHPart(i, l.SACCHPart(i))
	}
	if !bytes.Equal(re[:9], l[:9]) || re[9] != 0 {
		t.Errorf("sacch parts %x vs %x", re, l)
	}
}

func TestPayloadHeaderVectors(t *testing.T) {
	v := vectors(t)
	l := NewVCall(1234, 1997, true)
	h := BuildHeader(l, 5)
	if got := hex.EncodeToString(h[:]); got != v["net.header.ran5"] {
		t.Errorf("header %s want %s", got, v["net.header.ran5"])
	}
	tr := BuildHeader(l.TxRel(), 5)
	if got := hex.EncodeToString(tr[:]); got != v["net.trailer.ran5"] {
		t.Errorf("trailer %s want %s", got, v["net.trailer.ran5"])
	}
	pr, err := ParsePayload(h[:])
	if err != nil || pr.Kind != KindVCall || pr.L3.Src() != 1234 || pr.L3.Dst() != 1997 || !pr.SACCHOK || pr.SACCH.RAN() != 5 {
		t.Errorf("parse header %+v %v", pr, err)
	}
	pr, err = ParsePayload(tr[:])
	if err != nil || pr.Kind != KindTxRel {
		t.Errorf("parse trailer %+v %v", pr, err)
	}
}

func TestFramePacking(t *testing.T) {
	a := [7]byte{0xB9, 0xE8, 0x81, 0x52, 0x61, 0x73, 0x80}
	b := [7]byte{0x01, 0x23, 0x45, 0x67, 0x89, 0xAB, 0x80}
	h := PackFrames(a, b)
	// frame A occupies bits 0..48, frame B bits 49..97
	if h[0] != 0xB9 || h[5] != 0x73 || h[6]&0x80 != 0x80 {
		t.Errorf("A at start: %x", h)
	}
	a2, b2 := UnpackFrames(h[:])
	if a2 != a || b2 != b {
		t.Errorf("roundtrip %x %x", a2, b2)
	}
	if h[12]&0x3F != 0 || h[13] != 0 {
		t.Errorf("trailing pad bits not zero: %x", h)
	}
	// unused low bit of frame byte 6 is dropped
	c := [7]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	c2, _ := UnpackFrames(func() []byte { x := PackFrames(c, c); return x[:] }())
	if c2[6] != 0x80 {
		t.Errorf("bit 49 only: %x", c2)
	}
}

func TestBuildParseVoice(t *testing.T) {
	call := NewVCall(1234, 1997, true)
	var f [4][7]byte
	for i := range f {
		for j := range f[i] {
			f[i][j] = byte(i*16 + j)
		}
		f[i][6] &= 0x80
	}
	var re Layer3
	for pos := 0; pos < 4; pos++ {
		p := BuildVoice(call, 3, pos, f)
		pr, err := ParsePayload(p[:])
		if err != nil || pr.Kind != KindVoice || len(pr.Frames) != 4 || !pr.SACCHOK {
			t.Fatalf("pos %d: %+v %v", pos, pr, err)
		}
		for i := range f {
			if pr.Frames[i] != f[i] {
				t.Errorf("pos %d frame %d %x want %x", pos, i, pr.Frames[i], f[i])
			}
		}
		if pr.SACCH.Structure() != 3-pos || pr.SACCH.RAN() != 3 {
			t.Errorf("sacch %d %d", pr.SACCH.Structure(), pr.SACCH.RAN())
		}
		re.SetSACCHPart(pos, pr.SACCH.Data())
	}
	if re.Type() != MsgVCall || re.Src() != 1234 || re.Dst() != 1997 {
		t.Errorf("late entry from SACCH superframe: %x", re)
	}
}

func TestParseStolenFACCH(t *testing.T) {
	call := NewVCall(1234, 1997, true)
	var f [4][7]byte
	for _, opt := range []int{StealFACCH1First, StealFACCH1Second} {
		p := BuildVoice(call, 0, 0, f)
		p[0] = byte(NewLICH(RFCTRDCH, USCSACCHSS, opt, DirInbound))
		raw := FACCH1Raw(call.TxRel())
		start := 5
		if opt == StealFACCH1Second {
			start = 19
		}
		copy(p[start:], raw[:])
		pr, err := ParsePayload(p[:])
		if err != nil || pr.Kind != KindTxRel || len(pr.Frames) != 2 || !pr.HasL3 {
			t.Errorf("opt %d: %+v %v", opt, pr, err)
		}
	}
}

func TestParseRejects(t *testing.T) {
	if _, err := ParsePayload(make([]byte, 32)); err == nil {
		t.Error("short payload accepted")
	}
	h := BuildHeader(NewVCall(1, 2, true), 0)
	h[5] ^= 0xFF // both halves: break the first only; second still valid
	if pr, err := ParsePayload(h[:]); err != nil || pr.Kind != KindVCall {
		t.Errorf("second FACCH should rescue: %+v %v", pr, err)
	}
	h[19] ^= 0xFF
	if _, err := ParsePayload(h[:]); err == nil {
		t.Error("two bad FACCH1 accepted")
	}
	h = BuildHeader(NewVCall(1, 2, true), 0)
	h[0] = byte(NewLICH(RFCTRCCH, 0, 0, 0))
	if _, err := ParsePayload(h[:]); err == nil {
		t.Error("RCCH accepted")
	}
}

func TestScramble(t *testing.T) {
	f := make([]byte, FrameBytes)
	Scramble(f)
	if !bytes.Equal(f, scrambler[:]) {
		t.Error("scramble of zero is the table")
	}
	Scramble(f)
	if !bytes.Equal(f, make([]byte, FrameBytes)) {
		t.Error("scramble is an involution")
	}
}

// Frames with random bit errors (some uncorrectable) decode to exactly what
// the MMDVMHost decoders return, including the failing cases.
func TestDecodeMatchesMMDVMHostWithErrors(t *testing.T) {
	v := vectors(t)
	for i := 0; i < 24; i++ {
		key := fmt.Sprintf("rnd.facch1.%d", i)
		d, ok := DecodeFACCH1(hx(t, v[key+".frame"]), FACCH1Offset1)
		if got := fmt.Sprintf("ok=%d %x", b2i(ok), d); got != v[key+".decode"] {
			t.Errorf("%s: %q want %q", key, got, v[key+".decode"])
		}
		key = fmt.Sprintf("rnd.sacch.%d", i)
		s, ok := DecodeSACCH(hx(t, v[key+".frame"]))
		r := s.Raw()
		if got := fmt.Sprintf("ok=%d raw=%x", b2i(ok), r); got != v[key+".decode"] {
			t.Errorf("%s: %q want %q", key, got, v[key+".decode"])
		}
	}
}
