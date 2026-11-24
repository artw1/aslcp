// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// src/nlp.c at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

import "math"

// nlpState is nlp_t: the pitch estimator (non-linear pitch, NLP) memory.
type nlpState struct {
	w      [mPitch / dec]float32
	sq     [mPitch]float32
	sqFIR  [nDec]float32
	memX   float32
	memY   float32
	fftBuf [peFFT]float32
	fw     [peFFT/2 + 1]cpx
}

// init is nlp_init().
func (n *nlpState) init() {
	for i := 0; i < nDec; i++ {
		n.w[i] = math.Float32frombits(nlpWindow[i])
	}
}

// postProcessSubMultiples is post_process_sub_multiples(): search the
// sub-multiples of the global peak, favouring the previous frame's pitch.
func postProcessSubMultiples(fw *[peFFT/2 + 1]cpx, gmax float32, gmaxBin int, prevF0 float32) float32 {
	mult := 2
	const minBin = peFFT * dec / pMax
	cmaxBin := gmaxBin
	prevF0Bin := int(float32(prevF0*(peFFT*dec)) / sampRate)

	for gmaxBin/mult >= minBin {
		b := gmaxBin / mult
		bmin := int(0.8 * float64(b))
		bmax := int(1.2 * float64(b))
		if bmin < minBin {
			bmin = minBin
		}

		// lower threshold to favour previous frames pitch estimate
		var thresh float32
		if prevF0Bin > bmin && prevF0Bin < bmax {
			thresh = float32(cnlp * 0.5 * float64(gmax))
		} else {
			thresh = float32(cnlp * float64(gmax))
		}

		var lmax float32
		lmaxBin := bmin
		for b = bmin; b <= bmax; b++ {
			if fw[b].r > lmax {
				lmax = fw[b].r
				lmaxBin = b
			}
		}

		if lmax > thresh {
			if lmaxBin > 0 && lmaxBin < peFFT/2 &&
				lmax > fw[lmaxBin-1].r && lmax > fw[lmaxBin+1].r {
				cmaxBin = lmaxBin
			}
		}
		mult++
	}

	return float32(float32(float32(cmaxBin)*sampRate) / (peFFT * dec))
}

// run is nlp(): returns the pitch period in samples and updates prevF0.
func (n *nlpState) run(fft *rfft, sn *[mPitch]float32, prevF0 *float32) float32 {
	const d = nSamp / dec
	const startPos = mPitch - nSamp

	// Square, notch filter at DC, and LP filter vector
	for i := startPos; i < mPitch; i++ {
		n.sq[i] = float32(sn[i] * sn[i])
	}

	for i := startPos; i < mPitch; i++ { // notch filter at DC
		notch := n.sq[i] - n.memX
		// notch += COEFF * mem_y: COEFF is a double
		notch = float32(float64(notch) + float64(0.95*float64(n.memY)))
		n.memX = n.sq[i]
		n.memY = notch
		n.sq[i] = float32(float64(notch) + 1.0)
	}

	// decimating polyphase FIR filter: push left
	copy(n.sqFIR[:nDec-d], n.sqFIR[d:])

	for i := 0; i < d; i++ {
		idx := startPos + nlpNPh*i
		var acc float32
		for p := 0; p < nlpNPh; p++ {
			h := nlpFIRPoly[p*nlpTapPP : (p+1)*nlpTapPP]
			base := idx - p
			for t := 0; t < nlpTapPP; t++ {
				acc += float32(h[t] * n.sq[base-t*nlpNPh])
			}
		}
		n.sqFIR[nDec-d+i] = acc // d fresh samples
	}

	// Decimate and DFT
	for i := range n.fftBuf {
		n.fftBuf[i] = 0
	}
	for i := 0; i < nDec; i++ {
		n.fftBuf[i] = float32(n.sqFIR[i] * n.w[i])
	}

	fft.forward(&n.fftBuf, &n.fw)

	for i := 0; i < peFFT/2+1; i++ {
		n.fw[i].r = float32(n.fw[i].r*n.fw[i].r) + float32(n.fw[i].i*n.fw[i].i)
	}

	// find global peak
	const lo = peFFT * dec / pMax
	hi := peFFT * dec / pMin
	if hi > peFFT/2 {
		hi = peFFT / 2
	}
	var gmax float32
	gmaxBin := lo
	for i := lo; i <= hi; i++ {
		if n.fw[i].r > gmax {
			gmax = n.fw[i].r
			gmaxBin = i
		}
	}

	bestF0 := postProcessSubMultiples(&n.fw, gmax, gmaxBin, *prevF0)

	// Shift samples in buffer to make room for new samples
	copy(n.sq[:startPos], n.sq[nSamp:nSamp+startPos])

	*prevF0 = bestF0
	return float32(float32(sampRate) / bestF0)
}
