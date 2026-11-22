// SPDX-License-Identifier: GPL-3.0-or-later

package imbe

// Codec pairs an Encoder and a Decoder. It is what the engine's vocoder
// package uses for P25 and YSF VW. Not safe for concurrent use: use one
// Codec per call and direction.
type Codec struct {
	enc *Encoder
	dec *Decoder
}

// NewCodec returns a Codec with fresh encoder and decoder state.
func NewCodec() *Codec { return &Codec{enc: NewEncoder(), dec: NewDecoder()} }

// Encode turns 20 ms of 8 kHz PCM into one 11-byte IMBE frame.
func (c *Codec) Encode(pcm *[160]int16) [11]byte { return c.enc.Encode(pcm) }

// Decode turns one 11-byte IMBE frame into 20 ms of 8 kHz PCM.
func (c *Codec) Decode(f *[11]byte) [160]int16 { return c.dec.Decode(f) }
