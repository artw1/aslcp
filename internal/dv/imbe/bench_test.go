// SPDX-License-Identifier: GPL-3.0-or-later

package imbe

import (
	"math"
	"testing"
)

// BenchmarkCodecFrame is the CPU cost of one 20 ms frame in one direction
// pair: encode plus decode. The frame must cost well under 20 ms for the
// engine to run in real time.
func BenchmarkCodecFrame(b *testing.B) {
	c := NewCodec()
	var pcm [160]int16
	var f [11]byte
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for j := range pcm {
			pcm[j] = int16(6000*math.Sin(2*math.Pi*180*float64(i*160+j)/8000) + 2000*math.Sin(2*math.Pi*1100*float64(i*160+j)/8000))
		}
		f = c.Encode(&pcm)
		_ = c.Decode(&f)
	}
}
