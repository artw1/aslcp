// SPDX-License-Identifier: GPL-3.0-or-later

package codec2

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// The encoder goldens in testdata/ were produced by testdata/gen/gen.c linked
// against the unmodified Codec2-mod C sources; see testdata/README.md. The
// decoder goldens (dec.pcm.gz, rnddec.pcm.gz) are regression pins of this
// port's own output since the decoder was aligned with upstream libcodec2
// (whose decoder differs from Codec2-mod's); regenerate them with
// CODEC2_UPDATE_DEC_GOLDEN=1 go test. Agreement with libcodec2 itself is
// measured by .github/workflows/codec2-check.yml.

const (
	kFrames  = 512
	kSamples = kFrames * SamplesPerFrame
	inSHA256 = "7fe7a8bb5bd1e7656a9e9ac0ea3b4b1107829ebe6c0e94cdd49c065019d47f69"
)

// ---- synthetic test signal: mirrors gen_signal() in testdata/gen/gen.c ----

type lcg struct{ s uint32 }

func (l *lcg) next() uint32 {
	l.s = l.s*1103515245 + 12345
	return l.s
}

func isin(phase uint32) int32 {
	t := int32(phase) >> 16
	at := t
	if at < 0 {
		at = -at
	}
	y := int32((int64(4) * int64(t) * int64(32768-at)) >> 15)
	if y > 32767 {
		y = 32767
	}
	if y < -32767 {
		y = -32767
	}
	return y
}

func incFor(milliHz uint32) uint32 { return uint32((uint64(milliHz) << 32) / 8000000) }

func clip16(v int64) int16 {
	if v > 32767 {
		return 32767
	}
	if v < -32768 {
		return -32768
	}
	return int16(v)
}

const (
	segSil = iota
	segVoiced
	segHPNoise
	segTone
	segWhite
)

var formants = [2][3][3]int64{
	{{27075, -15386, 4695}, {18187, -15266, 13463}, {-14192, -14910, 45486}},
	{{31121, -15630, 893}, {-7355, -15146, 38885}, {-22017, -14794, 53195}},
}

type seg struct {
	fEnd, kind, f0a, f0b, vib, set, pulse, noise, gain int
}

var segs = []seg{
	{20, segSil, 0, 0, 0, 0, 0, 0, 0},
	{140, segVoiced, 100000, 160000, 0, 0, 4000, 0, 256},
	{160, segHPNoise, 0, 0, 0, 0, 0, 4000, 256},
	{260, segVoiced, 200000, 120000, 8000, 1, 10000, 0, 256},
	{280, segHPNoise, 0, 0, 0, 0, 0, 30, 256},
	{340, segVoiced, 130000, 90000, 0, 0, 4000, 0, 2560},
	{380, segVoiced, 140000, 140000, 0, 1, 8000, 2500, 256},
	{400, segTone, 440000, 0, 0, 0, 0, 12000, 256},
	{450, segVoiced, 65000, 90000, 3000, 0, 4000, 0, 256},
	{480, segWhite, 0, 0, 0, 0, 0, 0, 256},
	{512, segVoiced, 220000, 220000, 0, 1, 8000, 400, 256},
}

// sigGen returns the 512-frame synthetic speech-like test signal: silence,
// voiced vowels through formant resonators with gliding and vibrato pitch,
// noise bursts, a hard-clipped loud segment, a tone, full-scale white noise
// and a child-like voice. Integer arithmetic only, no libm.
func sigGen() []int16 {
	out := make([]int16, kSamples)
	var rng lcg
	rng.s = 1
	var phase, vph, tph uint32
	var y1, y2 [3]int64
	var prevX int32
	si := 0
	for n := 0; n < kSamples; n++ {
		f := n / 160
		for f >= segs[si].fEnd {
			si++
		}
		sg := &segs[si]
		segStart := 0
		if si > 0 {
			segStart = segs[si-1].fEnd * 160
		}
		segLen := sg.fEnd*160 - segStart
		noise := int32(rng.next()>>16) - 32768
		switch sg.kind {
		case segSil:
			out[n] = 0
		case segVoiced:
			pos := int64(n - segStart)
			f0 := int64(sg.f0a) + (int64(sg.f0b-sg.f0a)*pos)/int64(segLen)
			f0 += (int64(isin(vph)) * int64(sg.vib)) >> 15
			vph += incFor(5500)
			old := phase
			phase += incFor(uint32(f0))
			var x int64
			if phase < old {
				x = int64(sg.pulse)
			}
			x += (int64(noise) * int64(sg.noise)) >> 15
			for k := 0; k < 3; k++ {
				c := &formants[sg.set][k]
				y := (x*c[2] + c[0]*y1[k] + c[1]*y2[k]) >> 14
				if y > 1<<26 {
					y = 1 << 26
				}
				if y < -(1 << 26) {
					y = -(1 << 26)
				}
				y2[k] = y1[k]
				y1[k] = y
				x = y
			}
			out[n] = clip16((x * int64(sg.gain)) >> 8)
		case segHPNoise:
			x := int32((int64(noise) * int64(sg.noise)) >> 15)
			out[n] = clip16(int64(x - prevX))
			prevX = x
		case segTone:
			out[n] = clip16((int64(isin(tph)) * int64(sg.noise)) >> 15)
			tph += incFor(uint32(sg.f0a))
		case segWhite:
			out[n] = int16(noise)
		}
	}
	return out
}

func pcmBytes(p []int16) []byte {
	b := make([]byte, 2*len(p))
	for i, v := range p {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	return b
}

func readFile(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readGz(t testing.TB, name string) []byte {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(readFile(t, name)))
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// writeGz stores pcm as gzip-compressed little-endian samples in testdata.
func writeGz(t testing.TB, name string, pcm []int16) {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	b := make([]byte, 2*len(pcm))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	zw.Write(b)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join("testdata", name), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func toPCM(b []byte) []int16 {
	p := make([]int16, len(b)/2)
	for i := range p {
		p[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	return p
}

// encodeAll runs a fresh encoder over pcm.
func encodeAll(pcm []int16) []byte {
	enc := NewEncoder()
	out := make([]byte, 0, len(pcm)/SamplesPerFrame*BytesPerFrame)
	var in [SamplesPerFrame]int16
	var fr [BytesPerFrame]byte
	for i := 0; i+SamplesPerFrame <= len(pcm); i += SamplesPerFrame {
		copy(in[:], pcm[i:])
		enc.Encode(&fr, &in)
		out = append(out, fr[:]...)
	}
	return out
}

// decodeAll runs a fresh decoder over frames.
func decodeAll(frames []byte) []int16 {
	dec := NewDecoder()
	out := make([]int16, 0, len(frames)/BytesPerFrame*SamplesPerFrame)
	var fr [BytesPerFrame]byte
	var pcm [SamplesPerFrame]int16
	for i := 0; i+BytesPerFrame <= len(frames); i += BytesPerFrame {
		copy(fr[:], frames[i:])
		dec.Decode(&pcm, &fr)
		out = append(out, pcm[:]...)
	}
	return out
}

func TestInputSignal(t *testing.T) {
	sum := sha256.Sum256(pcmBytes(sigGen()))
	if got := hex.EncodeToString(sum[:]); got != inSHA256 {
		t.Fatalf("test signal differs from the one the goldens were made from: sha256 %s", got)
	}
}

func TestEncodeGolden(t *testing.T) {
	want := readFile(t, "enc.frames")
	got := encodeAll(sigGen())
	if len(got) != len(want) || len(want) != kFrames*BytesPerFrame {
		t.Fatalf("length %d, want %d", len(got), len(want))
	}
	bad := 0
	for f := 0; f < kFrames; f++ {
		g, w := got[f*8:f*8+8], want[f*8:f*8+8]
		if !bytes.Equal(g, w) {
			bad++
			if bad <= 5 {
				t.Errorf("frame %d: got %x want %x", f, g, w)
			}
		}
	}
	if bad > 0 {
		t.Errorf("%d of %d frames differ", bad, kFrames)
	}
}

// decodeDiff compares got with the reference output and returns the maximum
// absolute sample difference, the number of differing samples and the SNR of
// got against the reference in dB (+Inf when identical).
func decodeDiff(got, want []int16) (maxDiff, nDiff int, snr float64) {
	var sig, noise float64
	for i := range want {
		d := int(got[i]) - int(want[i])
		if d < 0 {
			d = -d
		}
		if d > maxDiff {
			maxDiff = d
		}
		if d != 0 {
			nDiff++
		}
		sig += float64(want[i]) * float64(want[i])
		noise += float64(d) * float64(d)
	}
	if noise == 0 {
		return maxDiff, nDiff, math.Inf(1)
	}
	return maxDiff, nDiff, 10 * math.Log10(sig/noise)
}

// The decoder calls sinf, cosf, powf and log10f at run time on arbitrary
// arguments. glibc's versions are not correctly rounded in rare cases, while
// the Go port evaluates them in double precision and rounds once (see
// doc.go), so a few samples may differ by one LSB from the glibc build.
// Measured on amd64: the speech vector has 1 differing sample (max diff 1,
// SNR 118.8 dB), the random vector has none. The limits below leave room for
// the same effect on other targets.
const (
	decMaxDiff        = 1
	decMaxDiffSamples = 16 // per vector
	decMinSNR         = 60.0
)

func TestDecodeGolden(t *testing.T) {
	for _, tc := range []struct{ name, in, out string }{
		{"speech", "enc.frames", "dec.pcm.gz"},
		{"random", "rnd.frames", "rnddec.pcm.gz"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeAll(readFile(t, tc.in))
			if os.Getenv("CODEC2_UPDATE_DEC_GOLDEN") != "" {
				writeGz(t, tc.out, got)
			}
			want := toPCM(readGz(t, tc.out))
			if len(got) != len(want) {
				t.Fatalf("length %d, want %d", len(got), len(want))
			}
			maxDiff, nDiff, snr := decodeDiff(got, want)
			t.Logf("max abs diff %d, %d of %d samples differ, SNR %.1f dB", maxDiff, nDiff, len(want), snr)
			if maxDiff > decMaxDiff || nDiff > decMaxDiffSamples || snr < decMinSNR {
				t.Errorf("decoder output outside tolerance (max diff <= %d, <= %d samples differ, SNR >= %.0f dB)",
					decMaxDiff, decMaxDiffSamples, decMinSNR)
			}
		})
	}
}

// snrDB is the signal to error ratio of got against ref in dB.
func snrDB(ref, got []int16) float64 {
	var sig, noise float64
	for i := range ref {
		d := float64(got[i]) - float64(ref[i])
		sig += float64(ref[i]) * float64(ref[i])
		noise += d * d
	}
	return 10 * math.Log10(sig/noise)
}

// TestRoundTripTone is a sanity check on a steady 440 Hz tone. Codec2
// resynthesises phases, so a waveform SNR against the input is meaningless;
// instead the decoded energy and the tone frequency (zero crossing rate)
// are compared. A pure sine is a poor fit for the speech model and the
// reference decoder itself comes out 14.52 dB below the input level for this
// signal (measured with the C build), so the level is checked against that
// value rather than against 0 dB.
func TestRoundTripTone(t *testing.T) {
	const frames = 100
	in := make([]int16, frames*SamplesPerFrame)
	for i := range in {
		in[i] = int16(math.Round(8000 * math.Sin(2*math.Pi*440*float64(i)/8000)))
	}
	out := decodeAll(encodeAll(in))
	if len(out) != len(in) {
		t.Fatalf("length %d, want %d", len(out), len(in))
	}
	// skip the first 10 frames (start-up) when measuring
	a, b := in[10*SamplesPerFrame:], out[10*SamplesPerFrame:]
	var ei, eo float64
	zi, zo := 0, 0
	for i := range a {
		ei += float64(a[i]) * float64(a[i])
		eo += float64(b[i]) * float64(b[i])
		if i > 0 {
			if (a[i-1] < 0) != (a[i] < 0) {
				zi++
			}
			if (b[i-1] < 0) != (b[i] < 0) {
				zo++
			}
		}
	}
	dB := 10 * math.Log10(eo/ei)
	t.Logf("level change %.2f dB, zero crossings in %d out %d", dB, zi, zo)
	if math.Abs(dB-(-14.52)) > 0.5 {
		t.Errorf("decoded level change %.2f dB, reference build gives -14.52 dB", dB)
	}
	if d := zo - zi; d < -zi/20 || d > zi/20 {
		t.Errorf("zero crossings %d vs %d: pitch not preserved", zo, zi)
	}
}

// TestStateIndependence runs two encoders (and then two decoders)
// interleaved on different signals and requires the same results as
// running them one after the other: no state is shared between instances.
func TestStateIndependence(t *testing.T) {
	sig := sigGen()
	other := make([]int16, len(sig))
	for i := range other {
		other[i] = sig[(i*7+12345)%len(sig)] // different, noisy content
	}

	want1 := encodeAll(sig)
	want2 := encodeAll(other)

	e1, e2 := NewEncoder(), NewEncoder()
	var in1, in2 [SamplesPerFrame]int16
	var f1, f2 [BytesPerFrame]byte
	var got1, got2 []byte
	for i := 0; i+SamplesPerFrame <= len(sig); i += SamplesPerFrame {
		copy(in1[:], sig[i:])
		copy(in2[:], other[i:])
		e1.Encode(&f1, &in1)
		e2.Encode(&f2, &in2)
		got1 = append(got1, f1[:]...)
		got2 = append(got2, f2[:]...)
	}
	if !bytes.Equal(got1, want1) || !bytes.Equal(got2, want2) {
		t.Fatal("interleaved encoders differ from sequential runs")
	}

	// same for decoders
	d1, d2 := NewDecoder(), NewDecoder()
	var o1, o2 [SamplesPerFrame]int16
	w1, w2 := decodeAll(got1), decodeAll(got2)
	for f := 0; f < kFrames; f++ {
		copy(f1[:], got1[f*8:])
		copy(f2[:], got2[f*8:])
		d1.Decode(&o1, &f1)
		d2.Decode(&o2, &f2)
		for i := range o1 {
			if o1[i] != w1[f*SamplesPerFrame+i] || o2[i] != w2[f*SamplesPerFrame+i] {
				t.Fatalf("interleaved decoders differ at frame %d sample %d", f, i)
			}
		}
	}
}

func TestExtremeFrames(t *testing.T) {
	var pcm [SamplesPerFrame]int16
	for _, fr := range [][BytesPerFrame]byte{
		{},
		{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff},
		{0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa, 0x55, 0xaa},
	} {
		fr := fr
		d := NewDecoder()
		for i := 0; i < 20; i++ {
			d.Decode(&pcm, &fr)
		}
	}

	// encoder: silence, all max, all min, alternating
	e := NewEncoder()
	var fr [BytesPerFrame]byte
	for _, v := range []int16{0, 32767, -32768} {
		for i := range pcm {
			pcm[i] = v
		}
		for i := 0; i < 20; i++ {
			e.Encode(&fr, &pcm)
		}
	}
	for i := range pcm {
		pcm[i] = 32767
		if i&1 == 1 {
			pcm[i] = -32768
		}
	}
	for i := 0; i < 20; i++ {
		e.Encode(&fr, &pcm)
	}
}

// TestRandomPCMStress feeds the encoder pseudo-random full-scale and
// low-level noise, then decodes, to catch index panics.
func TestRandomPCMStress(t *testing.T) {
	var rng lcg
	rng.s = 99
	e, d := NewEncoder(), NewDecoder()
	var pcm, out [SamplesPerFrame]int16
	var fr [BytesPerFrame]byte
	for f := 0; f < 400; f++ {
		shift := uint(f % 16)
		for i := range pcm {
			pcm[i] = int16(int32(rng.next()>>16) - 32768>>shift)
		}
		e.Encode(&fr, &pcm)
		d.Decode(&out, &fr)
	}
}

func FuzzDecode(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff})
	f.Add([]byte{0x80, 0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde})
	f.Fuzz(func(t *testing.T, b []byte) {
		d := NewDecoder()
		var fr [BytesPerFrame]byte
		var pcm [SamplesPerFrame]int16
		// decode the data as a sequence of frames so state carries over
		for i := 0; i+BytesPerFrame <= len(b); i += BytesPerFrame {
			copy(fr[:], b[i:])
			d.Decode(&pcm, &fr)
		}
		copy(fr[:], b)
		d.Decode(&pcm, &fr)
	})
}
