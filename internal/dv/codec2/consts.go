// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// inc/codec2_internal.h, src/util.c, inc/util.h at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

import "math"

// Sizes of the 3200 bit/s mode.
const (
	// SamplesPerFrame is the number of 8 kHz samples in one 20 ms frame.
	SamplesPerFrame = 160
	// BytesPerFrame is the size of one encoded frame (64 bits).
	BytesPerFrame = 8
)

// Codec2-mod's configuration (inc/codec2_internal.h).
const (
	nSamp    = 80  // N_SAMP: samples per 10 ms analysis frame
	mPitch   = 320 // M_PITCH: pitch analysis window
	pMin     = 20  // P_MIN
	pMax     = 160 // P_MAX
	nw       = 279 // NW: analysis window length
	tw       = 40  // TW: Parzen window ramp
	maxAmp   = 80  // MAX_AMP
	fftEnc   = 512 // FFT_ENC and FFT_DEC
	lspOrd   = 10  // LPC_ORD
	sampRate = 8000
	peFFT    = 512 // PE_FFT_SIZE
	dec      = 5   // DEC
	nDec     = mPitch / dec
	nlpNPh   = dec                  // NLP_NPHASE
	nlpTapPP = (48 + dec - 1) / dec // NLP_TAPS_PP

	woBits  = 7 // WO_BITS
	eBits   = 5 // E_BITS
	randMax = 32767
	wStart  = mPitch/2 - nw/2 // non-zero span of the analysis window
	wEnd    = wStart + nw
)

// Values that Codec2-mod writes as C double expressions. They are variables
// rather than Go constants because Go evaluates constant expressions
// exactly, whereas C rounds every intermediate to double; each one is then
// computed with the same float64 operations as the C code.
var (
	piD     = math.Pi         // M_PI
	twoPiD  = 2 * math.Pi     // TWO_PI = 2.0f * M_PI
	w0Min   = twoPiD / 160.0  // W0_MIN
	w0Max   = twoPiD / 20.0   // W0_MAX
	fftR    = twoPiD / fftEnc // FFT_R
	fft1R   = fftEnc / twoPiD // FFT_1_R
	pi095   = 0.95 * piD      // 0.95 * M_PI
	loPitch = twoPiD / pMax   // TWO_PI / P_MAX
	hiPitch = twoPiD / pMin   // TWO_PI / P_MIN

	// float constants (C "static const float" / float literals)
	vThreshLin = float32(math.Pow(10, float64(float32(6.0*float64(float32(0.1)))))) // V_THRESH_LIN = powf(10, V_THRESH*0.1f)
	sixtyHz    = float32(60.0 * twoPiD / sampRate)                                  // 60 Hz in rad/sample
	randK      = float32(twoPiD / randMax)                                          // TWO_PI / CODEC2_RAND_MAX
	lspEncK    = float32(float64(float32(4000)) / piD)                              // 4000.0f / M_PI
	lspDecK    = float32(piD / float64(float32(4000)))                              // M_PI / 4000.0f
	piHalf     = float32(piD / float64(float32(2)))                                 // M_PI / 2.0f
	lpcpfBeta  = 0.2                                                                // LPCPF_BETA
	bgBeta     = 0.1                                                                // BG_BETA
	cnlp       = 0.3                                                                // CNLP
	lpcCorrWo  = piD * float64(float32(150)) / float64(float32(4000))               // M_PI*150.0f/4000.0f
)

var (
	lpcpfTwoBeta = float32(2.0 * lpcpfBeta) // LPCPF_TWO_BETA, passed as float
	bgKeep       = 1.0 - bgBeta             // 1.0 - BG_BETA
	pMaxS        = float32(0.0200)          // P_MAX_S
)

// The C code calls libm's float functions; the Go port evaluates them in
// float64 and rounds once, which agrees with glibc except possibly in the
// last bit of a result on very rare inputs (see doc.go).

func cosf(x float32) float32  { return float32(math.Cos(float64(x))) }
func sinf(x float32) float32  { return float32(math.Sin(float64(x))) }
func sqrtf(x float32) float32 { return float32(math.Sqrt(float64(x))) }

func floorf(x float32) float32 { return float32(math.Floor(float64(x))) }

func log10f(x float32) float32 { return float32(math.Log10(float64(x))) }

// pow10f is powf(10.0f, x).
func pow10f(x float32) float32 { return float32(math.Pow(10, float64(x))) }

func fabsf(x float32) float32 { return float32(math.Abs(float64(x))) }

// ceilfFast is ceilf_fast() from util.h.
func ceilfFast(x float32) int {
	i := int(x)
	if float32(i) < x {
		i++
	}
	return i
}
