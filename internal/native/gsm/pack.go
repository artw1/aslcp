package gsm

import (
	"errors"
	"fmt"
)

// signature is the 4-bit magic that starts every packed frame.
const signature = 0xD

// larBits is the width of each coded log-area ratio.
var larBits = [8]uint{6, 6, 5, 5, 4, 4, 3, 3}

// ErrFrame reports a packed frame with a bad length or signature.
var ErrFrame = errors.New("gsm: invalid frame")

type bitWriter struct {
	buf [FrameBytes]byte
	pos uint // bits written
}

func (w *bitWriter) put(v int16, n uint) {
	for i := n; i > 0; i-- {
		if (v>>(i-1))&1 != 0 {
			w.buf[w.pos>>3] |= 0x80 >> (w.pos & 7)
		}
		w.pos++
	}
}

type bitReader struct {
	buf []byte
	pos uint
}

func (r *bitReader) get(n uint) int16 {
	var v int16
	for ; n > 0; n-- {
		v <<= 1
		if r.buf[r.pos>>3]&(0x80>>(r.pos&7)) != 0 {
			v |= 1
		}
		r.pos++
	}
	return v
}

// Pack packs the parameters into a 33-byte frame, most significant bit
// first, after the 0xD signature. Values are masked to their field width.
func (p *Params) Pack() [FrameBytes]byte {
	var w bitWriter
	w.put(signature, 4)
	for i, v := range p.LARc {
		w.put(v, larBits[i])
	}
	for j := 0; j < 4; j++ {
		w.put(p.Nc[j], 7)
		w.put(p.Bc[j], 2)
		w.put(p.Mc[j], 2)
		w.put(p.Xmaxc[j], 6)
		for _, v := range p.XMc[j] {
			w.put(v, 3)
		}
	}
	return w.buf
}

// Unpack parses a 33-byte packed frame.
func Unpack(frame []byte) (Params, error) {
	var p Params
	if len(frame) != FrameBytes {
		return p, fmt.Errorf("%w: %d bytes, want %d", ErrFrame, len(frame), FrameBytes)
	}
	if frame[0]>>4 != signature {
		return p, fmt.Errorf("%w: signature %#x", ErrFrame, frame[0]>>4)
	}
	r := bitReader{buf: frame, pos: 4}
	for i := range p.LARc {
		p.LARc[i] = r.get(larBits[i])
	}
	for j := 0; j < 4; j++ {
		p.Nc[j] = r.get(7)
		p.Bc[j] = r.get(2)
		p.Mc[j] = r.get(2)
		p.Xmaxc[j] = r.get(6)
		for i := range p.XMc[j] {
			p.XMc[j][i] = r.get(3)
		}
	}
	return p, nil
}
