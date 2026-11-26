package gsm

import (
	"bytes"
	"encoding/binary"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// The files in testdata were produced from speech.pcm (a synthetic voiced
// signal followed by silence, a full-scale square wave and full-scale noise)
// and random.gsm (random frames with a valid signature) with an independent
// GSM 06.10 reference implementation, so matching them proves bit-exactness.

func readPCM(t testing.TB, name string) [][FrameSamples]int16 {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	n := len(b) / (2 * FrameSamples)
	out := make([][FrameSamples]int16, n)
	if err := binary.Read(bytes.NewReader(b[:n*2*FrameSamples]), binary.LittleEndian, out); err != nil {
		t.Fatal(err)
	}
	return out
}

func readFrames(t testing.TB, name string) [][]byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	var out [][]byte
	for len(b) >= FrameBytes {
		out = append(out, b[:FrameBytes])
		b = b[FrameBytes:]
	}
	return out
}

func TestReferenceVectors(t *testing.T) {
	tests := []struct {
		name    string
		encode  string // input PCM to encode, or "" to decode only
		frames  string // expected encoder output, or decoder input
		decoded string // expected decoder output
	}{
		{"speech", "speech.pcm", "speech.gsm", "speech.dec"},
		{"random frames", "", "random.gsm", "random.dec"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frames := readFrames(t, tc.frames)
			if tc.encode != "" {
				pcm := readPCM(t, tc.encode)
				if len(pcm) != len(frames) {
					t.Fatalf("got %d input frames, %d reference frames", len(pcm), len(frames))
				}
				enc := NewEncoder()
				for i := range pcm {
					got := enc.Encode(&pcm[i])
					if !bytes.Equal(got[:], frames[i]) {
						t.Fatalf("frame %d:\n got %x\nwant %x", i, got, frames[i])
					}
				}
			}
			want := readPCM(t, tc.decoded)
			dec := NewDecoder()
			var got [FrameSamples]int16
			for i, f := range frames {
				if err := dec.Decode(f, &got); err != nil {
					t.Fatalf("frame %d: %v", i, err)
				}
				if got != want[i] {
					t.Fatalf("decoded frame %d differs:\n got %v\nwant %v", i, got, want[i])
				}
			}
		})
	}
}

func TestSilenceFrame(t *testing.T) {
	// Derived by hand from the quantizer tables: all-zero input gives
	// LARc = 32,32,20,11,8,5,3,2, Nc = 40, bc = Mc = xmaxc = 0 and every
	// pulse coded as 4 (zero).
	want := []byte{
		0xd8, 0x20, 0xa2, 0xe1, 0x5a,
		0x50, 0x00, 0x49, 0x24, 0x92, 0x49, 0x24,
		0x50, 0x00, 0x49, 0x24, 0x92, 0x49, 0x24,
		0x50, 0x00, 0x49, 0x24, 0x92, 0x49, 0x24,
		0x50, 0x00, 0x49, 0x24, 0x92, 0x49, 0x24,
	}
	enc := NewEncoder()
	var pcm [FrameSamples]int16
	for i := 0; i < 3; i++ {
		got := enc.Encode(&pcm)
		if !bytes.Equal(got[:], want) {
			t.Fatalf("frame %d: got %x, want %x", i, got, want)
		}
	}
}

func TestPackRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 200; i++ {
		var b [FrameBytes]byte
		r.Read(b[:])
		b[0] = 0xd0 | b[0]&0x0f
		p, err := Unpack(b[:])
		if err != nil {
			t.Fatal(err)
		}
		if got := p.Pack(); got != b {
			t.Fatalf("repack: got %x, want %x", got, b)
		}
	}
}

func TestUnpackErrors(t *testing.T) {
	good := make([]byte, FrameBytes)
	good[0] = 0xd0
	bad := append([]byte(nil), good...)
	bad[0] = 0xc0
	tests := []struct {
		name  string
		frame []byte
		ok    bool
	}{
		{"valid", good, true},
		{"empty", nil, false},
		{"short", good[:32], false},
		{"long", append(append([]byte(nil), good...), 0), false},
		{"bad signature", bad, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Unpack(tc.frame)
			if (err == nil) != tc.ok {
				t.Fatalf("err = %v, want ok=%v", err, tc.ok)
			}
		})
	}
}

// snrDB returns the signal-to-noise ratio of out against in, in dB.
func snrDB(in, out []int16) float64 {
	var sig, noise float64
	for i := range in {
		s := float64(in[i])
		d := s - float64(out[i])
		sig += s * s
		noise += d * d
	}
	if noise == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(sig/noise)
}

func TestRoundTripQuality(t *testing.T) {
	speech := readPCM(t, "speech.pcm")[:50] // the voiced part only
	tone := make([][FrameSamples]int16, 50)
	for i := range tone {
		for k := range tone[i] {
			n := float64(i*FrameSamples + k)
			tone[i][k] = int16(8000 * math.Sin(2*math.Pi*440*n/SampleRate))
		}
	}
	tests := []struct {
		name   string
		in     [][FrameSamples]int16
		minSNR float64
	}{
		// GSM full rate is a vocoder-like coder; waveform SNR on voiced
		// speech is typically a few dB to around 10 dB.
		{"speech-like", speech, 10},
		{"440 Hz tone", tone, 20},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			enc := NewEncoder()
			dec := NewDecoder()
			var in, out []int16
			var pcm [FrameSamples]int16
			for i := range tc.in {
				f := enc.Encode(&tc.in[i])
				if err := dec.Decode(f[:], &pcm); err != nil {
					t.Fatal(err)
				}
				in = append(in, tc.in[i][:]...)
				out = append(out, pcm[:]...)
			}
			// The codec adds no delay, so compare sample for sample after
			// skipping the first frames while the filters settle.
			got := snrDB(in[4*FrameSamples:], out[4*FrameSamples:])
			t.Logf("SNR %.1f dB", got)
			if got < tc.minSNR {
				t.Fatalf("SNR %.1f dB, want at least %.1f", got, tc.minSNR)
			}
		})
	}
}

func TestArbitraryInputNoPanic(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	enc := NewEncoder()
	dec := NewDecoder()
	var pcm, out [FrameSamples]int16
	for i := 0; i < 500; i++ {
		for k := range pcm {
			switch i % 4 {
			case 0:
				pcm[k] = int16(r.Uint32())
			case 1:
				pcm[k] = minWord
			case 2:
				pcm[k] = maxWord
			default:
				if k%2 == 0 {
					pcm[k] = minWord
				} else {
					pcm[k] = maxWord
				}
			}
		}
		enc.Encode(&pcm)
		var p Params
		for j := range p.XMc {
			p.Nc[j] = int16(r.Uint32())
			p.Bc[j] = int16(r.Uint32())
			p.Mc[j] = int16(r.Uint32())
			p.Xmaxc[j] = int16(r.Uint32())
			for k := range p.XMc[j] {
				p.XMc[j][k] = int16(r.Uint32())
			}
		}
		for j := range p.LARc {
			p.LARc[j] = int16(r.Uint32())
		}
		dec.DecodeParams(&p, &out)
		var b [FrameBytes]byte
		r.Read(b[:])
		_ = dec.Decode(b[:], &out)
	}
}

func TestReset(t *testing.T) {
	speech := readPCM(t, "speech.pcm")
	enc := NewEncoder()
	first := enc.Encode(&speech[10])
	enc.Encode(&speech[11])
	enc.Reset()
	if got := enc.Encode(&speech[10]); got != first {
		t.Fatal("encoder Reset did not restore the initial state")
	}
	dec := NewDecoder()
	var a, b [FrameSamples]int16
	_ = dec.Decode(first[:], &a)
	_ = dec.Decode(first[:], &b)
	dec.Reset()
	_ = dec.Decode(first[:], &b)
	if a != b {
		t.Fatal("decoder Reset did not restore the initial state")
	}
}

func BenchmarkEncode(b *testing.B) {
	speech := readPCM(b, "speech.pcm")
	enc := NewEncoder()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		enc.Encode(&speech[i%len(speech)])
	}
}

func BenchmarkDecode(b *testing.B) {
	frames := readFrames(b, "speech.gsm")
	dec := NewDecoder()
	var pcm [FrameSamples]int16
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = dec.Decode(frames[i%len(frames)], &pcm)
	}
}
