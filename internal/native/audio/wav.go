package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// maxWAV bounds the size of a WAV file read into memory.
const maxWAV = 16 << 20

// ParseWAV decodes a RIFF/WAVE file holding 8000 Hz mono 16-bit PCM and
// returns its samples. Unknown chunks are skipped; a data chunk longer than
// the file is truncated to what is there.
func ParseWAV(b []byte) ([]int16, error) {
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return nil, errors.New("audio: not a RIFF/WAVE file")
	}
	var haveFmt bool
	for p := 12; p+8 <= len(b); {
		id := string(b[p : p+4])
		size := int(binary.LittleEndian.Uint32(b[p+4 : p+8]))
		body := p + 8
		end := body + size
		if end > len(b) || end < body {
			end = len(b)
		}
		switch id {
		case "fmt ":
			if end-body < 16 {
				return nil, errors.New("audio: short fmt chunk")
			}
			tag := binary.LittleEndian.Uint16(b[body:])
			ch := binary.LittleEndian.Uint16(b[body+2:])
			rate := binary.LittleEndian.Uint32(b[body+4:])
			bits := binary.LittleEndian.Uint16(b[body+14:])
			if tag != 1 && tag != 0xFFFE {
				return nil, fmt.Errorf("audio: WAV format %d is not PCM", tag)
			}
			if ch != 1 || rate != SampleRate || bits != 16 {
				return nil, fmt.Errorf("audio: WAV must be 8000 Hz mono 16-bit, got %d Hz, %d channels, %d bits", rate, ch, bits)
			}
			haveFmt = true
		case "data":
			if !haveFmt {
				return nil, errors.New("audio: WAV data before fmt chunk")
			}
			n := (end - body) / 2
			out := make([]int16, n)
			for i := range out {
				out[i] = int16(binary.LittleEndian.Uint16(b[body+2*i:]))
			}
			return out, nil
		}
		p = end + size&1
	}
	return nil, errors.New("audio: WAV has no data chunk")
}

// EncodeWAV writes samples as an 8000 Hz mono 16-bit WAV file.
func EncodeWAV(s []int16) []byte {
	b := make([]byte, 44+2*len(s))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(s)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], SampleRate)
	binary.LittleEndian.PutUint32(b[28:], SampleRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(s)))
	for i, v := range s {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}

// WAVPlayer plays decoded samples as a Source.
type WAVPlayer struct {
	s   []int16
	pos int
}

// NewWAVPlayer returns a player for samples.
func NewWAVPlayer(samples []int16) *WAVPlayer { return &WAVPlayer{s: samples} }

// LoadWAV reads and parses a WAV file and returns a player for it.
func LoadWAV(path string) (*WAVPlayer, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("audio: open WAV: %w", err)
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, maxWAV+1))
	if err != nil {
		return nil, fmt.Errorf("audio: read WAV %s: %w", path, err)
	}
	if len(b) > maxWAV {
		return nil, fmt.Errorf("audio: WAV %s is too large", path)
	}
	s, err := ParseWAV(b)
	if err != nil {
		return nil, fmt.Errorf("audio: %s: %w", path, err)
	}
	return NewWAVPlayer(s), nil
}

// Next implements Source.
func (w *WAVPlayer) Next(f *Frame) bool {
	*f = Frame{}
	if w.pos >= len(w.s) {
		return false
	}
	n := copy(f[:], w.s[w.pos:])
	w.pos += n
	return true
}
