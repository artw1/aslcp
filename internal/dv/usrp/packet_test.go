package usrp

import (
	"bytes"
	"errors"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	var pcm [Samples]int16
	for i := range pcm {
		pcm[i] = int16(i*37 - 3000)
	}
	cases := []Packet{
		{Seq: 1, Keyup: true, Talkgroup: 9990, Type: TypeVoice, PCM: &pcm},
		{Seq: 2, Type: TypeVoice},
		{Seq: 3, Keyup: true, Type: TypeDTMF, DTMF: '5'},
		{Seq: 4, Type: TypeText, Text: MetaTLV("N0CALL 1997")},
		{Seq: 0xffffffff, Memory: 7, Type: TypeVoice},
	}
	for _, c := range cases {
		b := c.Append(nil)
		got, err := Parse(b)
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if !bytes.Equal(got.Append(nil), b) {
			t.Fatalf("reencode mismatch for %+v", c)
		}
		if got.Seq != c.Seq || got.Keyup != c.Keyup || got.Type != c.Type || got.Talkgroup != c.Talkgroup {
			t.Fatalf("got %+v want %+v", got, c)
		}
	}
}

func TestParseErrors(t *testing.T) {
	good := (&Packet{Type: TypeVoice}).Append(nil)
	cases := []struct {
		name string
		b    []byte
		want error
	}{
		{"nil", nil, ErrShort},
		{"short", good[:31], ErrShort},
		{"eye", append([]byte("XSRP"), good[4:]...), ErrEye},
		{"voice 10", append(append([]byte(nil), good...), make([]byte, 10)...), ErrPayload},
		{"voice 322", append(append([]byte(nil), good...), make([]byte, 322)...), ErrPayload},
		{"dtmf 2", append((&Packet{Type: TypeDTMF}).Append(nil), 1), ErrPayload},
	}
	for _, c := range cases {
		if _, err := Parse(c.b); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v want %v", c.name, err, c.want)
		}
	}
	bad := append([]byte(nil), good...)
	bad[23] = 9
	if _, err := Parse(bad); !errors.Is(err, ErrType) {
		t.Errorf("type: %v", err)
	}
}

func TestMetaTLV(t *testing.T) {
	b := MetaTLV("PARROT 9990")
	if b[0] != TLVSetInfo || int(b[1]) != len("PARROT 9990") {
		t.Fatalf("% x", b)
	}
	v, ok := ParseTLV(b, TLVSetInfo)
	if !ok || string(v) != "PARROT 9990" {
		t.Fatalf("%q %v", v, ok)
	}
	long := MetaTLV(string(bytes.Repeat([]byte("a"), 100)))
	if len(long) != 66 {
		t.Fatalf("len %d", len(long))
	}
	if v := MetaTLV("a\x00b\xff"); string(v[2:]) != "a?b?" {
		t.Fatalf("%q", v)
	}
	if _, ok := ParseTLV([]byte{8, 5, 1}, 8); ok {
		t.Fatal("truncated tlv accepted")
	}
}

func FuzzParse(f *testing.F) {
	var pcm [Samples]int16
	f.Add((&Packet{Keyup: true, Type: TypeVoice, PCM: &pcm}).Append(nil))
	f.Add((&Packet{Type: TypeText, Text: MetaTLV("x")}).Append(nil))
	f.Add((&Packet{Type: TypeDTMF, DTMF: '1'}).Append(nil))
	f.Add([]byte("USRP"))
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := Parse(b)
		if err != nil {
			return
		}
		out := p.Append(nil)
		q, err := Parse(out)
		if err != nil {
			t.Fatalf("reparse: %v", err)
		}
		if !bytes.Equal(q.Append(nil), out) {
			t.Fatal("unstable encoding")
		}
		ParseTLV(p.Text, TLVSetInfo)
	})
}
