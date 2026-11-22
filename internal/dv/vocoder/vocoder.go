// Package vocoder defines the voice codecs the engine uses and the md380-emu
// client that provides AMBE+2.
package vocoder

import "context"

const (
	PCMSamples = 160 // 20 ms at 8 kHz
	AMBEBytes  = 7   // AMBE+2 49 bits
	IMBEBytes  = 11  // IMBE 88 bits
)

// AMBE is the AMBE+2 vocoder (DMR, YSF, NXDN).
type AMBE interface {
	Encode(ctx context.Context, pcm *[PCMSamples]int16) ([AMBEBytes]byte, error)
	Decode(ctx context.Context, f [AMBEBytes]byte) ([PCMSamples]int16, error)
}

// IMBE is implemented by internal/dv/imbe (pure Go, stateful, not safe for
// concurrent use: one instance per direction and call).
// Bit packing: u0..u7 vectors (12,12,12,12,11,11,11,7 bits) MSB-first.
type IMBE interface {
	Encode(pcm *[PCMSamples]int16) [IMBEBytes]byte
	Decode(f *[IMBEBytes]byte) [PCMSamples]int16
}

// SilenceAMBE is the AMBE+2 frame that decodes to silence; it replaces a
// frame whose encode timed out.
var SilenceAMBE = [AMBEBytes]byte{0xb9, 0xe8, 0x81, 0x52, 0x61, 0x73, 0x00}

// Stats are the md380 client counters, summed over both directions.
type Stats struct {
	Frames   uint64 // requests answered by md380-emu
	Timeouts uint64 // requests that timed out or failed (replaced by silence)
	Down     bool   // too many consecutive timeouts
}
