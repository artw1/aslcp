// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// src/synthesis.c at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

import "math"

// makeSynthesisWindow is make_synthesis_window(): the Parzen window. The
// ramps accumulate in float as the C code does.
func makeSynthesisWindow(pn *[2 * nSamp]float32) {
	const n0 = nSamp / 2
	const n1 = 3 * nSamp / 2
	const step = 1.0 / (2 * tw)

	for i := range pn {
		pn[i] = 0
	}
	// ramp up
	var win float32
	for i := n0 - tw; i < n0+tw; i++ {
		pn[i] = win
		win = float32(float64(win) + step)
	}
	// flat
	for i := n0 + tw; i < n1-tw; i++ {
		pn[i] = 1.0
	}
	// ramp down
	win = 1.0
	for i := n1 - tw; i < n1+tw; i++ {
		pn[i] = win
		win = float32(float64(win) - step)
	}
}

// randPhase is TWO_PI * (float)codec2_rand() / CODEC2_RAND_MAX in double.
func randPhase(state *uint32) float32 {
	return float32(cTwoPi * float64(float32(codec2Rand(state))) / randMax)
}

// codec2Rand is codec2_rand().
func codec2Rand(state *uint32) int {
	*state = *state*1103515245 + 12345
	return int((*state >> 16) & 0x7FFF)
}

// samplePhase is sample_phase(): the synthesis filter phase at the harmonics.
func samplePhase(mo *model, h *[maxAmp + 1]cpx, a *[fftEnc/2 + 1]cpx) {
	r := float32(cTwoPi / fftEnc)

	for m := 1; m <= mo.l; m++ {
		// float division, as in C (m * Wo / r)
		b := int(float64(float32(m)*mo.wo/r) + 0.5)
		if b > fftEnc/2 {
			b = fftEnc / 2
		}
		// synth filter 1/A is opposite phase to analysis filter
		h[m] = cpx{a[b].r, -a[b].i}
	}
}

// phaseSynthZeroOrder is phase_synth_zero_order(): zero order phase model
// (excitation phase from the pitch pulse position), returning unit phasors.
func (d *Decoder) phaseSynthZeroOrder(mo *model, h *[maxAmp + 1]cpx, p *[maxAmp + 1]cpx) {
	// ex_phase += Wo * N_SAMP (float); then wrapped to +-pi in double.
	d.exPhase += float32(mo.wo * nSamp)
	fl := floorf(float32(float64(d.exPhase)/cTwoPi + 0.5))
	d.exPhase = float32(float64(d.exPhase) - cTwoPi*float64(fl))
	phi0 := d.exPhase

	for m := 1; m <= mo.l; m++ {
		var ph float32
		if mo.voiced {
			ph = float32(phi0 * float32(m))
		} else {
			ph = randPhase(&d.nextRn)
		}
		ex := cpx{r: cosf(ph), i: sinf(ph)}

		ar := float32(h[m].r*ex.r) - float32(h[m].i*ex.i)
		ai := float32(h[m].i*ex.r) + float32(h[m].r*ex.i)

		// model->phi = atan2f(A_.imag, A_.real + 1E-12); synthesis then
		// uses cosf/sinf of it.
		phi := float32(math.Atan2(float64(ai), float64(float32(float64(ar)+1e-12))))
		p[m] = cpx{r: cosf(phi), i: sinf(phi)}
	}
}

// postfilter is postfilter(): updates the background noise estimate and
// randomises the phase of harmonics below it.
func (d *Decoder) postfilter(mo *model, p *[maxAmp + 1]cpx) {
	// determine average energy across spectrum
	var e float32 = 1e-12
	for m := 1; m <= mo.l; m++ {
		e += float32(mo.a[m] * mo.a[m])
	}
	e = float32(10.0 * float64(log10f(float32(e/float32(mo.l)))))

	if !mo.voiced {
		// beneath threshold: update bg estimate (not during high level speech)
		if float64(e) < 40.0 { // BG_THRESH
			d.bgEst = float32(float64(float64(d.bgEst)*bgKeep) + float64(float64(e)*bgBeta))
		}
	} else {
		// make any harmonics less than the unvoiced background estimate
		// random phase
		thresh := pow10f(float32((float64(d.bgEst) + 6.0) / float64(float32(20.0))))

		for m := 1; m <= mo.l; m++ {
			if mo.a[m] < thresh {
				ph := randPhase(&d.nextRn)
				p[m] = cpx{r: cosf(ph), i: sinf(ph)}
			}
		}
	}
}

// earProtection is ear_protection(): attenuates frames over 30000, which
// are likely to be caused by bit errors.
func earProtection(inOut []float32) {
	var maxAbs float32
	for _, v := range inOut {
		if t := fabsf(v); t > maxAbs {
			maxAbs = t
		}
	}

	if maxAbs <= 30000.0 {
		return
	}

	over := maxAbs / 30000.0
	if over > 1.0 {
		gain := 1.0 / float32(over*over)
		for i := range inOut {
			inOut[i] = float32(inOut[i] * gain)
		}
	}
}

// synthesise is synthesise(): inverse DFT of the harmonics and overlap-add
// into the output memory (the shift == 1 path, the only one used).
func (d *Decoder) synthesise(mo *model, p *[maxAmp + 1]cpx) {
	sn := &d.sn

	// update memories
	copy(sn[:nSamp-1], sn[nSamp:2*nSamp-1])
	sn[nSamp-1] = 0.0

	sw := &d.sw
	*sw = [fftEnc/2 + 1]cpx{}

	for l := 1; l <= mo.l; l++ {
		// l * Wo * FFT_DEC is float, then / TWO_PI in double
		b := int(float64(float32(l)*mo.wo*fftEnc)/cTwoPi + 0.5)
		if b > fftEnc/2-1 {
			b = fftEnc/2 - 1
		}
		sw[b] = cpx{float32(mo.a[l] * p[l].r), float32(mo.a[l] * p[l].i)}
	}

	d.inv.inverse(sw, &d.swTime)
	t := &d.swTime

	// overlap add to previous samples
	for i := 0; i < nSamp-1; i++ {
		sn[i] += float32(t[fftEnc-nSamp+1+i] * d.pn[i])
	}

	for j := 0; j < nSamp+1; j++ {
		sn[nSamp-1+j] = float32(t[j] * d.pn[nSamp-1+j])
	}
}

// synthesiseOneFrame is synthesise_one_frame() with gain 1.0.
func (d *Decoder) synthesiseOneFrame(speech []int16, mo *model, aw *[fftEnc/2 + 1]cpx) {
	var p, h [maxAmp + 1]cpx

	samplePhase(mo, &h, aw)
	d.phaseSynthZeroOrder(mo, &h, &p)
	d.postfilter(mo, &p)
	d.synthesise(mo, &p)

	earProtection(d.sn[:nSamp])

	for i := 0; i < nSamp; i++ {
		v := d.sn[i]
		switch {
		case v > 32767.0:
			speech[i] = 32767
		case v < -32767.0:
			speech[i] = -32767
		case v != v: // NaN cannot occur for finite input; keep it defined
			speech[i] = 0
		default:
			speech[i] = int16(v)
		}
	}
}
