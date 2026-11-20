package helper

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"testing"
)

func TestFrameRoundTrip(t *testing.T) {
	var b bytes.Buffer
	if err := WriteData(&b, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := WriteResize(&b, 40, 132); err != nil {
		t.Fatal(err)
	}
	if err := WriteFrame(&b, FrameUser, []byte("alice")); err != nil {
		t.Fatal(err)
	}
	// wire format: type, two byte big-endian length, payload
	if got := b.Bytes()[:8]; !bytes.Equal(got, []byte{0, 0, 5, 'h', 'e', 'l', 'l', 'o'}) {
		t.Fatalf("data frame bytes % x", got)
	}
	typ, p, err := ReadFrame(&b)
	if err != nil || typ != FrameData || string(p) != "hello" {
		t.Fatalf("%d %q %v", typ, p, err)
	}
	typ, p, err = ReadFrame(&b)
	if err != nil || typ != FrameResize {
		t.Fatalf("%d %v", typ, err)
	}
	if r, c, err := ParseResize(p); err != nil || r != 40 || c != 132 {
		t.Fatalf("%d %d %v", r, c, err)
	}
	typ, p, err = ReadFrame(&b)
	if err != nil || typ != FrameUser || string(p) != "alice" {
		t.Fatalf("%d %q %v", typ, p, err)
	}
	if _, _, err = ReadFrame(&b); err != io.EOF {
		t.Fatalf("want EOF, got %v", err)
	}
}

func TestResizeIsNotData(t *testing.T) {
	// A resize frame and a data frame with the same four payload bytes differ
	// by type only, so input that looks like a resize stays input.
	var a, b bytes.Buffer
	_ = WriteResize(&a, 24, 80)
	var raw [4]byte
	binary.BigEndian.PutUint16(raw[0:2], 24)
	binary.BigEndian.PutUint16(raw[2:4], 80)
	_ = WriteData(&b, raw[:])
	ta, _, _ := ReadFrame(&a)
	tb, _, _ := ReadFrame(&b)
	if ta == tb {
		t.Fatal("resize and data frames have the same type")
	}
	if _, _, err := ParseResize([]byte{1, 2, 3}); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("short resize accepted: %v", err)
	}
}

func TestFrameLimitsAndErrors(t *testing.T) {
	if err := WriteFrame(io.Discard, FrameData, make([]byte, MaxFramePayload+1)); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("oversized frame written: %v", err)
	}
	// WriteData splits large input
	var b bytes.Buffer
	if err := WriteData(&b, make([]byte, 40<<10)); err != nil {
		t.Fatal(err)
	}
	n := 0
	for {
		_, p, err := ReadFrame(&b)
		if err != nil {
			break
		}
		if len(p) > dataChunk {
			t.Fatalf("chunk of %d", len(p))
		}
		n += len(p)
	}
	if n != 40<<10 {
		t.Fatalf("got %d bytes", n)
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{9, 0, 0})); !errors.Is(err, ErrBadFrame) {
		t.Fatalf("unknown type: %v", err)
	}
	if _, _, err := ReadFrame(bytes.NewReader([]byte{0, 0, 5, 'a'})); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncated: %v", err)
	}
}

func TestSizes(t *testing.T) {
	for _, c := range []struct {
		r, c uint16
		ok   bool
	}{{24, 80, true}, {1, 1, true}, {500, 500, true}, {0, 80, false}, {24, 0, false}, {501, 80, false}, {24, 501, false}} {
		if ValidSize(c.r, c.c) != c.ok {
			t.Errorf("ValidSize(%d,%d) != %v", c.r, c.c, c.ok)
		}
	}
	if r, c := ClampSize(0, 0); r != 24 || c != 80 {
		t.Fatalf("%d %d", r, c)
	}
	if r, c := ClampSize(9999, 7); r != 500 || c != 7 {
		t.Fatalf("%d %d", r, c)
	}
}

func TestParseProcUID(t *testing.T) {
	st := "Name:\tbash\nUmask:\t0022\nState:\tS (sleeping)\nUid:\t1001\t1001\t1001\t1001\nGid:\t1001\t1001\t1001\t1001\n"
	if uid, ok := parseProcUID(st); !ok || uid != 1001 {
		t.Fatalf("%d %v", uid, ok)
	}
	if _, ok := parseProcUID("Name:\tx\n"); ok {
		t.Fatal("no Uid line accepted")
	}
}

func utmpRec(typ int16, line, user string) []byte {
	b := make([]byte, utmpRecSize)
	binary.LittleEndian.PutUint16(b[0:2], uint16(typ))
	copy(b[utmpLineOff:], line)
	copy(b[utmpUserOff:], user)
	return b
}

func TestUtmpUser(t *testing.T) {
	var data []byte
	data = append(data, utmpRec(2, "~", "reboot")...)
	data = append(data, utmpRec(8, "pts/3", "dead")...)
	data = append(data, utmpRec(7, "pts/2", "other")...)
	data = append(data, utmpRec(7, "pts/3", "example")...)
	if got := utmpUser(data, "pts/3"); got != "example" {
		t.Fatalf("got %q", got)
	}
	if got := utmpUser(data, "pts/9"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := utmpUser(data[:100], "pts/3"); got != "" {
		t.Fatalf("short data: %q", got)
	}
}
