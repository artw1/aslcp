// SPDX-License-Identifier: GPL-3.0-or-later

package m17

import (
	"bytes"
	"encoding/hex"
	"errors"
	"math/rand"
	"testing"
)

// Expected values below were computed independently (Python, from the
// alphabet and the spec's encoding rule), not with this package.
func TestCallsignVectors(t *testing.T) {
	for _, c := range []struct {
		s string
		a Address
	}{
		{"A", 1},
		{"N0CALL", 0x4b13d106},
		{"N1CALL", 0x4b13d12e},
		{"M17-XXX C", 0x11f91caa7aed},
		{"N0CALL H", 0x1317813d106},
		{".........", AddrStandardMax},
	} {
		a, err := EncodeCallsign(c.s)
		if err != nil || a != c.a {
			t.Errorf("Encode(%q) = %#x, %v; want %#x", c.s, a, err, c.a)
		}
		s, err := DecodeCallsign(c.a)
		if err != nil || s != c.s {
			t.Errorf("Decode(%#x) = %q, %v; want %q", c.a, s, err, c.s)
		}
	}
	if a, err := EncodeCallsign("n0call"); err != nil || a != 0x4b13d106 {
		t.Errorf("lowercase fold: %#x %v", a, err)
	}
}

func TestCallsignErrors(t *testing.T) {
	for _, c := range []struct {
		s   string
		err error
	}{
		{"", ErrEmptyCallsign},
		{"ABCDEFGHIJ", ErrCallsignLength},
		{"N0CALL!", ErrCallsignChar},
		{"N0_CALL", ErrCallsignChar},
		{"N0CALL ", ErrCallsignSpace},
		{"é", ErrCallsignChar},
	} {
		if _, err := EncodeCallsign(c.s); !errors.Is(err, c.err) {
			t.Errorf("Encode(%q) err = %v, want %v", c.s, err, c.err)
		}
	}
	for _, c := range []struct {
		a   Address
		err error
	}{
		{AddrReserved, ErrAddrReserved},
		{AddrExtendedMin, ErrAddrExtended},
		{AddrBroadcast - 1, ErrAddrExtended},
		{AddrBroadcast + 1, ErrAddrOutOfRange},
	} {
		if _, err := DecodeCallsign(c.a); !errors.Is(err, c.err) {
			t.Errorf("Decode(%#x) err = %v, want %v", c.a, err, c.err)
		}
	}
	if s, err := DecodeCallsign(AddrBroadcast); err != nil || s != BroadcastName {
		t.Errorf("broadcast = %q, %v", s, err)
	}
	if AddrBroadcast.String() != "BROADCAST" || AddrReserved.String() != "0x000000000000" {
		t.Error("Address.String")
	}
}

func TestCallsignRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 20000; i++ {
		n := 1 + r.Intn(MaxCallsign)
		b := make([]byte, n)
		for j := range b {
			b[j] = Alphabet[r.Intn(40)]
		}
		if b[n-1] == ' ' {
			b[n-1] = 'X'
		}
		a, err := EncodeCallsign(string(b))
		if err != nil || a == 0 || a > AddrStandardMax {
			t.Fatalf("%q: %#x %v", b, a, err)
		}
		s, err := DecodeCallsign(a)
		if err != nil || s != string(b) {
			t.Fatalf("%q -> %#x -> %q, %v", b, a, s, err)
		}
		if AddressFromBytes(func() []byte { x := a.Bytes(); return x[:] }()) != a {
			t.Fatal("bytes round trip")
		}
	}
}

func TestCRCVectors(t *testing.T) {
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	for _, c := range []struct {
		in   []byte
		want uint16
	}{
		{nil, 0xFFFF},
		{[]byte("A"), 0x206E},
		{[]byte("123456789"), 0x772B},
		{all, 0x1C31},
	} {
		if got := CRC16(c.in); got != c.want {
			t.Errorf("CRC16(%q...) = %#04x, want %#04x", c.in[:min(len(c.in), 9)], got, c.want)
		}
	}
}

func TestType(t *testing.T) {
	ty, err := VoiceType(3)
	if err != nil || ty != 0x0185 {
		t.Fatalf("VoiceType(3) = %#x, %v", ty, err)
	}
	if !ty.Stream() || ty.DataType() != DataTypeVoice || ty.CAN() != 3 || ty.Encrypted() || ty.Signed() || ty.Reserved() || !ty.IsVoice() {
		t.Errorf("accessors on %#x", ty)
	}
	if _, err := VoiceType(16); !errors.Is(err, ErrCAN) {
		t.Error("CAN 16 accepted")
	}
	if !Type(0x0005|1<<3).Encrypted() || !Type(1<<11).Signed() || !Type(0x1000).Reserved() {
		t.Error("flag accessors")
	}
}

func goldenStream() Stream {
	dst, _ := EncodeCallsign("M17-XXX C")
	src, _ := EncodeCallsign("N0CALL")
	ty, _ := VoiceType(3)
	s := Stream{StreamID: 0x1234, LSF: LSF{Dst: dst, Src: src, Type: ty}, FN: 0x8001}
	for i := range s.Payload {
		s.Payload[i] = byte(i)
	}
	return s
}

// Golden bytes assembled and CRC'd independently in Python.
const goldenHex = "4d313720123411f91caa7aed00004b13d106018500000000000000000000000000008001000102030405060708090a0b0c0d0e0f5c44"

func TestStreamGolden(t *testing.T) {
	s := goldenStream()
	b := s.Marshal()
	if hex.EncodeToString(b[:]) != goldenHex {
		t.Fatalf("marshal = %x", b)
	}
	got, err := UnmarshalStream(b[:])
	if err != nil || got != s {
		t.Fatalf("unmarshal = %+v, %v", got, err)
	}
	if !got.Last() || got.Counter() != 1 {
		t.Error("FN accessors")
	}
	if NextFN(0x7FFF) != 0 || NextFN(5) != 6 || NextFN(0x8005) != 6 {
		t.Error("NextFN")
	}
}

func TestStreamMalformed(t *testing.T) {
	s := goldenStream()
	g := s.Marshal()
	if _, err := UnmarshalStream(g[:53]); !errors.Is(err, ErrStreamLength) {
		t.Error("short")
	}
	if _, err := UnmarshalStream(append(g[:], 0)); !errors.Is(err, ErrStreamLength) {
		t.Error("long (55 bytes)")
	}
	b := g
	b[3] = 'P'
	if _, err := UnmarshalStream(b[:]); !errors.Is(err, ErrStreamMagic) {
		t.Error("magic")
	}
	b = g
	b[40] ^= 1
	got, err := UnmarshalStream(b[:])
	if !errors.Is(err, ErrStreamCRC) || got.StreamID != 0x1234 {
		t.Errorf("crc: %v %+v", err, got)
	}
	if IsStream([]byte("M17P")) || !IsStream(g[:]) {
		t.Error("IsStream")
	}
}

func TestStreamRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 5000; i++ {
		var s Stream
		s.StreamID = uint16(r.Uint32())
		s.LSF.Dst = Address(r.Uint64() & 0xFFFFFFFFFFFF)
		s.LSF.Src = Address(r.Uint64() & 0xFFFFFFFFFFFF)
		s.LSF.Type = Type(r.Uint32())
		r.Read(s.LSF.Meta[:])
		s.FN = uint16(r.Uint32())
		r.Read(s.Payload[:])
		b := s.Marshal()
		got, err := UnmarshalStream(b[:])
		if err != nil || got != s {
			t.Fatalf("round trip %d: %v", i, err)
		}
	}
}

func TestLSF(t *testing.T) {
	g := goldenStream().LSF
	g.Meta[0] = 0xAA
	g.Meta[13] = 0x55
	b := g.MarshalCRC()
	got, err := UnmarshalLSFCRC(b[:])
	if err != nil || got != g {
		t.Fatalf("%+v %v", got, err)
	}
	b[5] ^= 1
	if _, err := UnmarshalLSFCRC(b[:]); !errors.Is(err, ErrLSFCRC) {
		t.Error("crc")
	}
	if _, err := UnmarshalLSFCRC(b[:29]); !errors.Is(err, ErrLSFLength) {
		t.Error("len crc form")
	}
	if _, err := UnmarshalLSF(b[:]); !errors.Is(err, ErrLSFLength) {
		t.Error("len")
	}
	d, full := g.Marshal(), g.MarshalCRC()
	if !bytes.Equal(d[:], full[:28]) {
		t.Error("LSD prefix")
	}
}

func TestControlGolden(t *testing.T) {
	a, _ := EncodeCallsign("N0CALL")
	for _, c := range []struct {
		c   Control
		hex string
	}{
		{Conn(a, 'C'), "434f4e4e00004b13d10643"},
		{Lstn(a, 'Z'), "4c53544e00004b13d1065a"},
		{Pong(a), "504f4e4700004b13d106"},
		{Disc(a), "4449534300004b13d106"},
		{Control{Kind: KindPING, Addr: a}, "50494e4700004b13d106"},
		{Control{Kind: KindDISC, Short: true}, "44495343"},
		{Control{Kind: KindACKN, Short: true}, "41434b4e"},
		{Control{Kind: KindNACK, Short: true}, "4e41434b"},
	} {
		b, err := c.c.Marshal()
		if err != nil || hex.EncodeToString(b) != c.hex {
			t.Errorf("%v: %x %v want %s", c.c, b, err, c.hex)
			continue
		}
		got, err := UnmarshalControl(b)
		if err != nil || got != c.c {
			t.Errorf("%v: unmarshal %+v %v", c.c, got, err)
		}
	}
}

func TestControlErrors(t *testing.T) {
	a := Address(1)
	if _, err := Conn(a, 'a').Marshal(); !errors.Is(err, ErrModule) {
		t.Error("module")
	}
	if _, err := (Control{Kind: KindACKN}).Marshal(); !errors.Is(err, ErrControlKind) {
		t.Error("ackn long")
	}
	if _, err := (Control{Kind: "XXXX"}).Marshal(); !errors.Is(err, ErrControlMagic) {
		t.Error("kind")
	}
	for _, c := range []struct {
		b   string
		err error
	}{
		{"", ErrControlLength},
		{"434f4e", ErrControlLength},
		{"434f4e4e00004b13d106", ErrControlLength}, // CONN w/o module
		{"434f4e4e00004b13d10600", ErrModule},      // module 0
		{"434f4e4e00004b13d10631", ErrModule},      // module '1'
		{"41434b4e00", ErrControlLength},           // ACKN 5
		{"4e41434b00004b13d106", ErrControlLength}, // peer NACK
		{"504f4e47", ErrControlLength},             // PONG short
		{"4449534300", ErrControlLength},           // DISC 5
		{"5a5a5a5a", ErrControlMagic},
	} {
		raw, _ := hex.DecodeString(c.b)
		if _, err := UnmarshalControl(raw); !errors.Is(err, c.err) {
			t.Errorf("%s: %v want %v", c.b, err, c.err)
		}
	}
	long := append([]byte("CONN"), make([]byte, 33)...)
	if _, err := UnmarshalControl(long); !errors.Is(err, ErrControlLength) {
		t.Error("interlink")
	}
}

func TestTarget(t *testing.T) {
	r, m, err := ParseTarget("M17-XXX C")
	if err != nil || r != "M17-XXX" || m != 'C' {
		t.Fatalf("%q %c %v", r, m, err)
	}
	if r, m, err := ParseTarget("M17-0A9 Z"); err != nil || r != "M17-0A9" || m != 'Z' {
		t.Fatal(r, m, err)
	}
	for _, s := range []string{"", "disconnect", "M17-XXX", "M17-XXX  C", "M17-XXX c", "m17-xxx C", "M17-XX C", "M17-XXXX C", "M17-XXX CC", " M17-XXX C", "M17-XXX C\n", "M17-XXX 1", "M17_XXX C"} {
		if _, _, err := ParseTarget(s); !errors.Is(err, ErrTarget) {
			t.Errorf("%q accepted: %v", s, err)
		}
	}
	s, err := FormatTarget("M17-XXX", 'C')
	if err != nil || s != "M17-XXX C" {
		t.Error(s, err)
	}
	if _, err := FormatTarget("M17-XXX", 'c'); err == nil {
		t.Error("bad module formatted")
	}
}

func FuzzUnmarshalStream(f *testing.F) {
	g := goldenStream()
	b := g.Marshal()
	f.Add(b[:])
	f.Add([]byte("M17 "))
	f.Fuzz(func(t *testing.T, in []byte) {
		s, err := UnmarshalStream(in)
		if err == nil {
			out := s.Marshal()
			if !bytes.Equal(out[:], in) {
				t.Fatalf("re-marshal differs")
			}
		}
	})
}

func FuzzUnmarshalControl(f *testing.F) {
	for _, h := range []string{"434f4e4e00004b13d10643", "44495343", "504f4e4700004b13d106"} {
		b, _ := hex.DecodeString(h)
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		c, err := UnmarshalControl(in)
		if err == nil {
			out, err := c.Marshal()
			if err != nil || !bytes.Equal(out, in) {
				t.Fatalf("re-marshal %x -> %x %v", in, out, err)
			}
		}
	})
}

func FuzzUnmarshalLSF(f *testing.F) {
	g := goldenStream().LSF
	b := g.MarshalCRC()
	f.Add(b[:])
	f.Fuzz(func(t *testing.T, in []byte) {
		if l, err := UnmarshalLSFCRC(in); err == nil {
			out := l.MarshalCRC()
			if !bytes.Equal(out[:], in) {
				t.Fatal("re-marshal differs")
			}
		}
		UnmarshalLSF(in)
	})
}

func FuzzTargetAndCallsign(f *testing.F) {
	f.Add("M17-XXX C")
	f.Add("N0CALL")
	f.Fuzz(func(t *testing.T, s string) {
		if r, m, err := ParseTarget(s); err == nil {
			if g, err := FormatTarget(r, m); err != nil || g != s {
				t.Fatalf("%q", s)
			}
		}
		if a, err := EncodeCallsign(s); err == nil {
			if d, err := DecodeCallsign(a); err != nil || len(d) == 0 {
				t.Fatalf("%q", s)
			}
		}
	})
}
