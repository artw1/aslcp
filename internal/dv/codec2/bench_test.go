// SPDX-License-Identifier: GPL-3.0-or-later

package codec2

import "testing"

// Both benchmarks report ns per 20 ms frame (one iteration is one frame).

func BenchmarkEncode(b *testing.B) {
	sig := sigGen()
	e := NewEncoder()
	var in [SamplesPerFrame]int16
	var fr [BytesPerFrame]byte
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		off := (i % kFrames) * SamplesPerFrame
		copy(in[:], sig[off:])
		e.Encode(&fr, &in)
	}
}

func BenchmarkDecode(b *testing.B) {
	frames := readFile(b, "enc.frames")
	d := NewDecoder()
	var fr [BytesPerFrame]byte
	var out [SamplesPerFrame]int16
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		off := (i % kFrames) * BytesPerFrame
		copy(fr[:], frames[off:])
		d.Decode(&out, &fr)
	}
}
