package helper

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// Terminal stream framing (docs/PLAN-1.3.0.md 8.4). After the JSON reply of
// terminal.open the connection carries frames in both directions: one type
// byte, a two byte big-endian length, then the payload.
const (
	// FrameData carries terminal bytes (keyboard input towards the helper,
	// program output towards the web server).
	FrameData byte = 0
	// FrameResize carries the window size, rows then columns as two big-endian
	// uint16 (web server to helper only).
	FrameResize byte = 1
	// FrameUser carries the Linux user name once login has authenticated it
	// (helper to web server only, at most once). Unknown: never sent.
	FrameUser byte = 2
	// FrameEnd asks for the session to be ended (empty payload; client to
	// helper, and helper to term-host). A stream that just closes is a
	// transport loss and only detaches the session.
	FrameEnd byte = 3
)

// MaxFramePayload is the largest payload of one frame (two byte length).
const MaxFramePayload = 0xFFFF

// dataChunk is the largest data payload WriteData puts in one frame.
const dataChunk = 16 << 10

// Terminal window size limits.
const (
	DefaultRows = 24
	DefaultCols = 80
	MaxRows     = 500
	MaxCols     = 500
)

// ErrBadFrame is returned for a frame that violates the protocol.
var ErrBadFrame = errors.New("bad terminal frame")

// ValidSize reports whether rows and cols are within bounds (1..500).
func ValidSize(rows, cols uint16) bool {
	return rows >= 1 && rows <= MaxRows && cols >= 1 && cols <= MaxCols
}

// ClampSize returns a usable size: zero means the default, anything above
// the maximum is cut to it.
func ClampSize(rows, cols uint16) (uint16, uint16) {
	if rows == 0 {
		rows = DefaultRows
	}
	if cols == 0 {
		cols = DefaultCols
	}
	if rows > MaxRows {
		rows = MaxRows
	}
	if cols > MaxCols {
		cols = MaxCols
	}
	return rows, cols
}

// WriteFrame writes one frame with a single Write call.
func WriteFrame(w io.Writer, typ byte, payload []byte) error {
	if len(payload) > MaxFramePayload {
		return fmt.Errorf("%w: payload of %d bytes", ErrBadFrame, len(payload))
	}
	b := make([]byte, 3+len(payload))
	b[0] = typ
	binary.BigEndian.PutUint16(b[1:3], uint16(len(payload)))
	copy(b[3:], payload)
	_, err := w.Write(b)
	return err
}

// WriteData writes data as one or more data frames.
func WriteData(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n := len(data)
		if n > dataChunk {
			n = dataChunk
		}
		if err := WriteFrame(w, FrameData, data[:n]); err != nil {
			return err
		}
		data = data[n:]
	}
	return nil
}

// WriteResize writes a resize frame.
func WriteResize(w io.Writer, rows, cols uint16) error {
	var p [4]byte
	binary.BigEndian.PutUint16(p[0:2], rows)
	binary.BigEndian.PutUint16(p[2:4], cols)
	return WriteFrame(w, FrameResize, p[:])
}

// ParseResize decodes the payload of a resize frame.
func ParseResize(p []byte) (rows, cols uint16, err error) {
	if len(p) != 4 {
		return 0, 0, fmt.Errorf("%w: resize payload of %d bytes", ErrBadFrame, len(p))
	}
	return binary.BigEndian.Uint16(p[0:2]), binary.BigEndian.Uint16(p[2:4]), nil
}

// ReadFrame reads one frame. The returned payload is a fresh slice. An
// unknown type is ErrBadFrame; a clean end of stream between frames is
// io.EOF.
func ReadFrame(r io.Reader) (typ byte, payload []byte, err error) {
	var h [3]byte
	if _, err = io.ReadFull(r, h[:]); err != nil {
		return 0, nil, err
	}
	typ = h[0]
	if typ != FrameData && typ != FrameResize && typ != FrameUser && typ != FrameEnd {
		return 0, nil, fmt.Errorf("%w: type %d", ErrBadFrame, typ)
	}
	n := int(binary.BigEndian.Uint16(h[1:3]))
	payload = make([]byte, n)
	if _, err = io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return 0, nil, err
	}
	return typ, payload, nil
}
