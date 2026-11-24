// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"fmt"
	"math"
	"math/cmplx"
	"sort"
	"strings"
)

// Diagnostics for the decoder comparison: SNR per second (drift versus
// bursts) and a per-frame log-spectral distance (a real spectral error
// versus mere phase differences).

const (
	segSamples = 8000 // one second at 8 kHz
	lsdN       = 256  // DFT size for the spectral comparison
	lsdLoBin   = 3    // about 94 Hz
	lsdHiBin   = 120  // about 3750 Hz
	lsdFloor   = 1.0  // magnitude floor, avoids log(0) in silence
)

// fftPow2 is an in-place radix-2 FFT; len(x) must be a power of two.
func fftPow2(x []complex128) {
	n := len(x)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i]
		}
	}
	for l := 2; l <= n; l <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(l)))
		for i := 0; i < n; i += l {
			wk := complex(1, 0)
			for k := 0; k < l/2; k++ {
				u, v := x[i+k], x[i+k+l/2]*wk
				x[i+k], x[i+k+l/2] = u+v, u-v
				wk *= w
			}
		}
	}
}

// magSpectrum returns the Hann-windowed magnitude spectrum (bins 0..N/2) of
// one frame of fs samples.
func magSpectrum(s []int16) []float64 {
	x := make([]complex128, lsdN)
	for i := 0; i < fs && i < len(s); i++ {
		w := 0.5 - 0.5*math.Cos(2*math.Pi*(float64(i)+0.5)/fs)
		x[i] = complex(float64(s[i])*w, 0)
	}
	fftPow2(x)
	m := make([]float64, lsdN/2+1)
	for i := range m {
		m[i] = cmplx.Abs(x[i])
	}
	return m
}

// lsd returns the log-spectral distance in dB (RMS over the bins) between
// two frames.
func lsd(a, b []int16) float64 {
	ma, mb := magSpectrum(a), magSpectrum(b)
	var sum float64
	for k := lsdLoBin; k <= lsdHiBin; k++ {
		d := 20 * math.Log10(math.Max(ma[k], lsdFloor)/math.Max(mb[k], lsdFloor))
		sum += d * d
	}
	return math.Sqrt(sum / float64(lsdHiBin-lsdLoBin+1))
}

// diagnose renders the per-second SNR and per-frame spectral comparison of
// two decodes of the same bits.
func diagnose(name string, ref, ours []int16) string {
	var b strings.Builder
	n := min(len(ref), len(ours))
	fmt.Fprintf(&b, "#### %s: decoder diagnostics (ours vs c2dec on identical bits)\n\n", name)

	var segs []string
	for i := 0; i+segSamples <= n; i += segSamples {
		segs = append(segs, fmtSNR(snr(ref[i:i+segSamples], ours[i:i+segSamples])))
	}
	fmt.Fprintf(&b, "SNR per second: %s\n\n", strings.Join(segs, ", "))

	type fr struct {
		idx int
		lsd float64
		snr float64
	}
	var frames []fr
	var sum float64
	for i := 0; (i+1)*fs <= n; i++ {
		r, o := ref[i*fs:(i+1)*fs], ours[i*fs:(i+1)*fs]
		frames = append(frames, fr{i, lsd(r, o), snr(r, o)})
		sum += frames[len(frames)-1].lsd
	}
	if len(frames) == 0 {
		return b.String()
	}
	maxL, over3, firstBad := 0.0, 0, -1
	for _, f := range frames {
		maxL = math.Max(maxL, f.lsd)
		if f.lsd > 3 {
			over3++
		}
		if firstBad < 0 && f.snr < 10 {
			firstBad = f.idx
		}
	}
	fmt.Fprintf(&b, "Log-spectral distance per 20 ms frame (bins %d-%d of %d, dB): mean %.2f, max %.2f, frames over 3 dB: %d/%d; first frame with SNR < 10 dB: %d\n\n",
		lsdLoBin, lsdHiBin, lsdN, sum/float64(len(frames)), maxL, over3, len(frames), firstBad)
	sort.Slice(frames, func(i, j int) bool { return frames[i].lsd > frames[j].lsd })
	fmt.Fprintln(&b, "Worst frames: frame (time s): LSD dB / frame SNR dB")
	fmt.Fprintln(&b)
	for _, f := range frames[:min(8, len(frames))] {
		fmt.Fprintf(&b, "- %d (%.2f s): %.2f / %.1f\n", f.idx, float64(f.idx)*fs/8000, f.lsd, f.snr)
	}
	fmt.Fprintln(&b)
	return b.String()
}
