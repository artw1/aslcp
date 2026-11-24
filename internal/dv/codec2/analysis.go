// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// src/analysis.c at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

import "math"

// model holds the sinusoidal model parameters of one 10 ms frame
// (model_t).
type model struct {
	wo     float32
	l      int
	a      [maxAmp + 1]float32
	voiced bool
}

// makeAnalysisWindow is make_analysis_window(): the time-domain Hann
// window w and its frequency response W.
func (e *Encoder) makeAnalysisWindow() {
	const mp2, nw2, fe2 = mPitch / 2, nw / 2, fftEnc / 2

	var m float32
	for j := 0; j < nw; j++ {
		val := math.Float32frombits(analysisHann[j])
		e.w[mp2-nw2+j] = val
		m += float32(val * val)
	}

	scale := 1.0 / sqrtf(float32(m*fftEnc))
	for i := range e.w {
		e.w[i] *= scale
	}

	// circular shift
	var wshift [fftEnc]float32
	for i := 0; i < nw2; i++ {
		wshift[i] = e.w[i+mp2]
	}
	for i, j := fftEnc-nw2, mp2-nw2; i < fftEnc; i, j = i+1, j+1 {
		wshift[i] = e.w[j]
	}

	var wf [fftEnc/2 + 1]cpx
	e.fft.forward(&wshift, &wf)
	for i := 0; i < fe2; i++ {
		e.bigW[i] = wf[fe2-i].r
		e.bigW[i+fe2] = wf[i].r
	}
}

// hsPitchRefinement is hs_pitch_refinement(): harmonic-sum search for the Wo
// that maximises the energy at the harmonics.
func hsPitchRefinement(mo *model, sw *[fftEnc]cpx, pmin, pmax, pstep float32) {
	mo.l = int(piD / float64(mo.wo))
	wom := mo.wo
	var em float32

	for p := pmin; p <= pmax; p += pstep {
		var en float32
		wo := float32(twoPiD / float64(p))

		bFloat := float32(float64(wo) * fft1R)
		currentB := bFloat

		for m := 1; m <= mo.l; m++ {
			b := int(currentB + 0.5)
			s := sw[b]
			en += float32(s.r*s.r) + float32(s.i*s.i)
			currentB += bFloat
		}

		if en > em {
			em = en
			wom = wo
		}
	}
	mo.wo = wom
}

// twoStagePitchRefinement is two_stage_pitch_refinement().
func twoStagePitchRefinement(mo *model, sw *[fftEnc]cpx) {
	// Coarse refinement
	pmax := float32(twoPiD/float64(mo.wo) + 5)
	pmin := float32(twoPiD/float64(mo.wo) - 5)
	hsPitchRefinement(mo, sw, pmin, pmax, 1.0)

	// Fine refinement
	pmax = float32(twoPiD/float64(mo.wo) + 1)
	pmin = float32(twoPiD/float64(mo.wo) - 1)
	hsPitchRefinement(mo, sw, pmin, pmax, 0.25)

	// Limit range
	if float64(mo.wo) < loPitch {
		mo.wo = float32(loPitch)
	}
	if float64(mo.wo) > hiPitch {
		mo.wo = float32(hiPitch)
	}

	mo.l = int(floorf(float32(piD / float64(mo.wo))))

	// trap occasional round off issues with floorf()
	if float64(float32(mo.wo*float32(mo.l))) >= pi095 {
		mo.l--
	}
}

// estimateAmplitudes is estimate_amplitudes().
func estimateAmplitudes(mo *model, sw *[fftEnc]cpx) {
	for m := 1; m <= mo.l; m++ {
		var den float32

		// (m - 0.5f) * Wo is float; times FFT_1_R (double) plus 0.5f is
		// double.
		ta := float32(float32(float32(m)-0.5) * mo.wo)
		tb := float32(float32(float32(m)+0.5) * mo.wo)
		am := int(float64(float64(ta)*fft1R) + 0.5)
		bm := int(float64(float64(tb)*fft1R) + 0.5)
		if am < 0 {
			am = 0
		}
		if bm > fftEnc/2 {
			bm = fftEnc / 2
		}
		for i := am; i < bm; i++ {
			den += float32(sw[i].r*sw[i].r) + float32(sw[i].i*sw[i].i)
		}
		mo.a[m] = sqrtf(den)
	}
}

// estVoicingMbe is est_voicing_mbe(): voiced/unvoiced decision.
func estVoicingMbe(mo *model, sw *[fftEnc]cpx, w *[fftEnc]float32) {
	wo := mo.wo
	woBin := float32(float64(float32(wo*fftEnc)) / twoPiD)

	// Ratio of low to high frequency energy, to catch gross errors.
	l1000 := int(float32(float32(mo.l)*1000.0) / (sampRate / 2))
	l2000 := int(float32(float32(mo.l)*2000.0) / (sampRate / 2))
	l4000 := int(float32(float32(mo.l)*4000.0) / (sampRate / 2))

	var sig, elow, ehigh float32 = 1e-4, 1e-4, 1e-4
	for l := 1; l <= l4000; l++ {
		t := float32(mo.a[l] * mo.a[l])
		if l <= l2000 {
			elow += t
			if l <= l1000 {
				sig += t
			}
		}
		if l >= l2000 {
			ehigh += t
		}
	}

	var errAcc float32 = 1e-4 // accumulated error between original and synthesised

	// Just test across the harmonics in the first 1000 Hz
	for l := 1; l <= l1000; l++ {
		var am cpx
		var den float32

		fl := float32(l)
		al := ceilfFast(float32((fl - 0.5) * woBin))
		bl := ceilfFast(float32((fl + 0.5) * woBin))

		// centres Hw[] about current harmonic
		offset := int(float64(float32(fftEnc/2)-float32(fl*woBin)) + 0.5)
		if offset < -al {
			offset = -al
		}
		if offset+bl >= fftEnc {
			offset = fftEnc - bl - 1
		}

		if al < 0 {
			al = 0
		}
		if bl > fftEnc {
			bl = fftEnc
		}

		for m := al; m < bl; m++ {
			wv := w[offset+m]
			am.r += float32(sw[m].r * wv)
			am.i += float32(sw[m].i * wv)
			den += float32(wv * wv)
		}

		if den < 1e-12 {
			continue
		}

		am.r = am.r / den
		am.i = am.i / den

		// error between estimated harmonic and original
		for m := al; m < bl; m++ {
			wv := w[offset+m]
			dr := sw[m].r - float32(am.r*wv)
			di := sw[m].i - float32(am.i*wv)
			errAcc += float32(dr*dr) + float32(di*di)
		}
	}

	mo.voiced = sig > float32(vThreshLin*errAcc)

	// Type 1 errors: strongly voiced speech accidentally declared unvoiced
	if !mo.voiced {
		if elow > float32(10.0*ehigh) {
			mo.voiced = true
		}
	}

	// Type 2 errors: strongly unvoiced speech accidentally declared voiced
	if mo.voiced {
		if elow < float32(0.1*ehigh) {
			mo.voiced = false
		}
		// low (50 Hz) pitch estimates for unvoiced speech
		if elow < float32(0.39810717055349725077*ehigh) && mo.wo <= sixtyHz {
			mo.voiced = false
		}
	}
}

// dftSpeech is dft_speech(): windowed 512-point DFT of the centre of sn,
// bins 0..256 plus the Hermitian mirror in 257..511 (the pitch refinement
// deliberately reads past Nyquist).
func (e *Encoder) dftSpeech(sw *[fftEnc]cpx) {
	var x [fftEnc]float32
	for i := 0; i < nw/2; i++ {
		x[i] = float32(e.sn[i+mPitch/2] * e.w[i+mPitch/2])
	}
	for i := 0; i < nw/2; i++ {
		x[fftEnc-nw/2+i] = float32(e.sn[i+mPitch/2-nw/2] * e.w[i+mPitch/2-nw/2])
	}

	e.fft.forward(&x, (*[fftEnc/2 + 1]cpx)(sw[:fftEnc/2+1]))
	for k := 1; k < fftEnc/2; k++ {
		sw[fftEnc-k] = cpx{sw[k].r, -sw[k].i}
	}
}

// analyseOneFrame is analyse_one_frame(): one 10 ms analysis step.
func (e *Encoder) analyseOneFrame(mo *model, speech []int16) {
	copy(e.sn[:mPitch-nSamp], e.sn[nSamp:])
	for i := 0; i < nSamp; i++ {
		e.sn[i+mPitch-nSamp] = float32(speech[i])
	}

	sw := &e.sw
	e.dftSpeech(sw)

	// Estimate pitch
	pitch := e.nlp.run(&e.fft, &e.sn, &e.prevF0)
	mo.wo = float32(twoPiD / float64(pitch))
	mo.l = int(piD / float64(mo.wo))

	// estimate model parameters
	twoStagePitchRefinement(mo, sw)
	estimateAmplitudes(mo, sw)
	estVoicingMbe(mo, sw, &e.bigW)
}
