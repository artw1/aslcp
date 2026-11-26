package iax2

import (
	"bytes"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestFullFrameRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		f    FullFrame
		hdr  []byte // expected first 12 bytes
	}{
		{"new", FullFrame{SrcCall: 0x1234, DstCall: 0, TS: 3, OSeq: 0, ISeq: 0, Type: TypeIAX, Subclass: CmdNew, Data: []byte{1, 2}},
			[]byte{0x92, 0x34, 0, 0, 0, 0, 0, 3, 0, 0, 6, 1}},
		{"retrans", FullFrame{SrcCall: 1, DstCall: 0x7fff, Retrans: true, TS: 0x01020304, OSeq: 5, ISeq: 9, Type: TypeVoice, Subclass: FormatULaw},
			[]byte{0x80, 1, 0xff, 0xff, 1, 2, 3, 4, 5, 9, 2, 4}},
		{"compressed subclass", FullFrame{SrcCall: 2, DstCall: 3, Type: TypeVoice, Subclass: 1 << 8},
			[]byte{0x80, 2, 0, 3, 0, 0, 0, 0, 0, 0, 2, 0x88}},
		{"text", FullFrame{SrcCall: 2, DstCall: 3, TS: 70000, OSeq: 255, ISeq: 254, Type: TypeText, Data: []byte("L \x00")},
			[]byte{0x80, 2, 0, 3, 0, 1, 0x11, 0x70, 255, 254, 7, 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.f.Append(nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(b[:12], tc.hdr) {
				t.Fatalf("header % x, want % x", b[:12], tc.hdr)
			}
			full, mini, err := Decode(b)
			if err != nil || mini != nil {
				t.Fatalf("decode: %v %v", mini, err)
			}
			want := tc.f
			if want.Data == nil {
				want.Data = []byte{}
			}
			if !reflect.DeepEqual(*full, want) {
				t.Fatalf("got %v, want %v", full, &want)
			}
		})
	}
}

func TestMiniFrameRoundTrip(t *testing.T) {
	m := MiniFrame{SrcCall: 0x0abc, TS: 0xfffe, Data: []byte{0xff, 0x7f}}
	b := m.Append(nil)
	if !bytes.Equal(b, []byte{0x0a, 0xbc, 0xff, 0xfe, 0xff, 0x7f}) {
		t.Fatalf("% x", b)
	}
	full, mini, err := Decode(b)
	if err != nil || full != nil || !reflect.DeepEqual(*mini, m) {
		t.Fatalf("got %v %v %v", full, mini, err)
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"empty", nil, ErrShort},
		{"short mini", []byte{0, 1, 2}, ErrShort},
		{"short full", []byte{0x80, 1, 0, 0, 0}, ErrShort},
		{"meta", []byte{0, 0, 0x80, 1, 0, 0}, ErrMeta},
	}
	for _, tc := range cases {
		if _, _, err := Decode(tc.b); !errors.Is(err, tc.want) {
			t.Errorf("%s: err %v, want %v", tc.name, err, tc.want)
		}
	}
	if _, _, err := Decode([]byte{0x80, 1, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0xff}); err == nil {
		t.Error("subclass shift 127 accepted")
	}
	if _, err := (&FullFrame{Subclass: 0x81}).Append(nil); err == nil {
		t.Error("non power of two subclass encoded")
	}
}

func TestIEsRoundTrip(t *testing.T) {
	var l IEs
	l.AddU16(IEVersion, 2)
	l.AddString(IECalledNumber, "2000")
	l.AddU32(IEFormat, FormatULaw)
	l.AddFormat2(IEFormat2, uint64(FormatULaw))
	l.AddBytes(IECallToken, nil)
	l.AddU8(IECauseCode, 16)
	b, err := l.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{11, 2, 0, 2, 1, 4, '2', '0', '0', '0', 9, 4, 0, 0, 0, 4,
		56, 9, 0, 0, 0, 0, 0, 0, 0, 0, 4, 54, 0, 42, 1, 16}
	if !bytes.Equal(b, want) {
		t.Fatalf("encode % x\nwant   % x", b, want)
	}
	p, err := ParseIEs(b)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := p.U16(IEVersion); !ok || v != 2 {
		t.Errorf("version %d %v", v, ok)
	}
	if p.String(IECalledNumber) != "2000" {
		t.Errorf("called %q", p.String(IECalledNumber))
	}
	if f, ok := p.Format(); !ok || f != uint64(FormatULaw) {
		t.Errorf("format %x", f)
	}
	if tok, ok := p.Get(IECallToken); !ok || len(tok) != 0 {
		t.Errorf("token %v %v", tok, ok)
	}
	if _, ok := p.Get(IEChallenge); ok {
		t.Error("absent IE found")
	}
	for _, bad := range [][]byte{{1}, {1, 5, 'a'}} {
		if _, err := ParseIEs(bad); err == nil {
			t.Errorf("% x parsed", bad)
		}
	}
	if _, err := (IEs{{Type: 1, Data: make([]byte, 256)}}).Encode(); err == nil {
		t.Error("oversized IE encoded")
	}
}

func TestFormatFallback(t *testing.T) {
	var l IEs
	l.AddU32(IEFormat, FormatGSM)
	if f, ok := l.Format(); !ok || f != uint64(FormatGSM) {
		t.Fatalf("format %x %v", f, ok)
	}
}

func TestPackDateTime(t *testing.T) {
	ts := time.Date(2026, 11, 26, 13, 45, 31, 0, time.UTC)
	got := PackDateTime(ts)
	want := uint32(26)<<25 | 11<<21 | 26<<16 | 13<<11 | 45<<5 | 15
	if got != want {
		t.Fatalf("got %08x want %08x", got, want)
	}
}

func TestMD5Response(t *testing.T) {
	// RFC 1321 test vector: MD5("abc").
	if got := MD5Response("ab", "c"); got != "900150983cd24fb0d6963f7d28e17f72" {
		t.Fatal(got)
	}
}

func TestULawKnownValues(t *testing.T) {
	dec := []struct {
		u uint8
		v int16
	}{
		{0x00, -32124}, {0x80, 32124}, {0xff, 0}, {0x7f, 0}, {0xfe, 8}, {0x7e, -8},
		{0xf0, 120}, {0xef, 132}, {0x70, -120}, {0x8f, 16764}, {0x0f, -16764},
	}
	for _, tc := range dec {
		if got := ULawDecode(tc.u); got != tc.v {
			t.Errorf("decode %#02x = %d, want %d", tc.u, got, tc.v)
		}
	}
	enc := []struct {
		v int16
		u uint8
	}{
		{0, 0xff}, {-1, 0x7f}, {32767, 0x80}, {-32768, 0x00}, {8, 0xfe}, {-8, 0x7e},
		{120, 0xf0}, {132, 0xef}, {1000, 0xce},
	}
	for _, tc := range enc {
		if got := ULawEncode(tc.v); got != tc.u {
			t.Errorf("encode %d = %#02x, want %#02x", tc.v, got, tc.u)
		}
	}
	// Every code except negative zero survives decode and encode.
	for i := 0; i < 256; i++ {
		u := uint8(i)
		if u == 0x7f {
			continue
		}
		if got := ULawEncode(ULawDecode(u)); got != u {
			t.Errorf("round trip %#02x -> %d -> %#02x", u, ULawDecode(u), got)
		}
	}
}

func TestULawFrames(t *testing.T) {
	var f Frame
	for i := range f {
		f[i] = int16(i * 100)
	}
	b := EncodeFrame(nil, &f)
	if len(b) != FrameSamples {
		t.Fatalf("len %d", len(b))
	}
	out := DecodeULaw(append(b, b[:10]...))
	if len(out) != 2 {
		t.Fatalf("frames %d", len(out))
	}
	for i := range f {
		if out[0][i] != ULawDecode(ULawEncode(f[i])) {
			t.Fatalf("sample %d", i)
		}
	}
	if out[1][9] != out[0][9] || out[1][10] != 0 {
		t.Fatal("short frame not padded")
	}
}
