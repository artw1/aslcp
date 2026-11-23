// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// src/lpc.c, inc/lpc.h at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

// autocorrelate is autocorrelate() for the non-zero span of the window.
func autocorrelate(sn *[mPitch]float32, rn *[lspOrd + 1]float32) {
	for j := 0; j < lspOrd+1; j++ {
		var acc float32
		for i := wStart; i < wEnd-j; i++ {
			acc += float32(sn[i] * sn[i+j])
		}
		rn[j] = acc
	}
}

// levinsonDurbin is levinson_durbin().
func levinsonDurbin(r *[lspOrd + 1]float32, lpcs *[lspOrd + 1]float32) {
	var a, aPrev [lspOrd + 1]float32

	a[0] = 1.0
	e := r[0]

	for i := 1; i <= lspOrd; i++ {
		var sum float32
		for j := 1; j <= i-1; j++ {
			sum += float32(aPrev[j] * r[i-j])
		}

		k := -(r[i] + sum) / e

		if fabsf(k) > 1.0 {
			k = 0.0
		}

		a[i] = k

		for j := 1; j <= i-1; j++ {
			a[j] = aPrev[j] + float32(k*aPrev[i-j])
		}

		e *= 1.0 - float32(k*k)

		for j := 1; j <= i; j++ {
			aPrev[j] = a[j]
		}
	}

	lpcs[0] = 1.0
	for i := 1; i <= lspOrd; i++ {
		lpcs[i] = a[i]
	}
}

// chebPolyEva is cheb_poly_eva(), unrolled for LPC_ORD = 10.
func chebPolyEva(coef *[lspOrd + 1]float32, x float32) float32 {
	t0 := float32(1.0)
	t1 := x

	sum := float32(coef[5]*t0) + float32(coef[4]*t1)

	twoX := float32(2.0 * x)

	t2 := float32(twoX*t1) - t0
	sum += float32(coef[3] * t2)

	t3 := float32(twoX*t2) - t1
	sum += float32(coef[2] * t3)

	t4 := float32(twoX*t3) - t2
	sum += float32(coef[1] * t4)

	t5 := float32(twoX*t4) - t3
	sum += float32(coef[0] * t5)

	return sum
}

// lpcToLSP is lpc_to_lsp() with 6 bisections; it returns the number of
// roots found.
func lpcToLSP(a *[lspOrd + 1]float32, freq *[lspOrd]float32) int {
	var q, p [lspOrd + 1]float32
	roots := 0

	const m = lspOrd / 2

	p[0], q[0] = 1.0, 1.0
	for i := 1; i <= m; i++ {
		p[i] = a[i] + a[lspOrd+1-i] - p[i-1]
		q[i] = a[i] - a[lspOrd+1-i] + q[i-1]
	}

	for i := 0; i < m; i++ {
		p[i] *= 2.0
		q[i] *= 2.0
	}

	xl := float32(1.0)
	delta := float32(0.01) // LSP_DELTA1

	for j := 0; j < lspOrd; j++ {
		pt := &p
		if j&1 != 0 {
			pt = &q
		}

		xr := xl
		psuml := chebPolyEva(pt, xl)

		for xr >= -1.0 {
			xr = xl - delta
			psumr := chebPolyEva(pt, xr)

			if (psumr <= 0.0 && psuml >= 0.0) || (psumr >= 0.0 && psuml <= 0.0) {
				roots++

				var xm float32
				for it := 0; it < 6; it++ {
					xm = float32(0.5 * (xl + xr))
					psumm := chebPolyEva(pt, xm)
					if float32(psumm*psuml) > 0.0 {
						xl = xm
						psuml = psumm
					} else {
						xr = xm
					}
				}

				freq[j] = xm
				xl = xm
				break
			}
			psuml = psumr
			xl = xr
		}
	}

	for i := 0; i < lspOrd; i++ {
		freq[i] = fastAcosf(freq[i])
	}
	return roots
}

// lspToLPC is lsp_to_lpc().
func lspToLPC(lsp *[lspOrd]float32, ak *[lspOrd + 1]float32) {
	var freq [lspOrd]float32
	var wp [2*lspOrd + 2]float32

	for i := 0; i < lspOrd; i++ {
		freq[i] = fastCosf(lsp[i])
	}

	xin1, xin2 := float32(1.0), float32(1.0)

	for j := 0; j <= lspOrd; j++ {
		var n4 int
		for i := 0; i < lspOrd/2; i++ {
			n1 := i * 4
			n2, n3 := n1+1, n1+2
			n4 = n1 + 3
			xout1 := xin1 - float32(float32(2*freq[2*i])*wp[n1]) + wp[n2]
			xout2 := xin2 - float32(float32(2*freq[2*i+1])*wp[n3]) + wp[n4]
			wp[n2] = wp[n1]
			wp[n4] = wp[n3]
			wp[n1] = xin1
			wp[n3] = xin2
			xin1 = xout1
			xin2 = xout2
		}

		xout1 := xin1 + wp[n4+1]
		xout2 := xin2 - wp[n4+2]

		ak[j] = float32(float64(xout1+xout2) * 0.5)

		wp[n4+1] = xin1
		wp[n4+2] = xin2

		xin1 = 0.0
		xin2 = 0.0
	}
}

// speechToUqLSPs is speech_to_uq_lsps(): LPC analysis of the windowed
// speech, unquantised LSPs and the residual energy.
func (e *Encoder) speechToUqLSPs(lsp *[lspOrd]float32, ak *[lspOrd + 1]float32) float32 {
	var wn [mPitch]float32
	var en float32
	for i := wStart; i < wEnd; i++ {
		wn[i] = float32(e.sn[i] * e.w[i])
		en += float32(wn[i] * wn[i])
	}

	// trap 0 energy case as LPC analysis will fail
	if en < 1e-6 { // LPC_ENERGY_FLOOR
		for i := 0; i < lspOrd; i++ {
			lsp[i] = float32(piD / lspOrd * float64(float32(i)))
		}
		*ak = [lspOrd + 1]float32{1.0}
		return 0.0
	}

	var r [lspOrd + 1]float32
	autocorrelate(&wn, &r)
	levinsonDurbin(&r, ak)

	var energy float32
	for i := 0; i <= lspOrd; i++ {
		energy += float32(ak[i] * r[i])
	}

	// 15 Hz bandwidth expansion, after the energy calculation
	for i := 0; i <= lspOrd; i++ {
		ak[i] = float32(ak[i] * bwGamma[i])
	}

	roots := lpcToLSP(ak, lsp)
	if roots != lspOrd {
		// if root finding fails use some benign LSP values instead
		for i := 0; i < lspOrd; i++ {
			lsp[i] = float32(piD / lspOrd * float64(float32(i)))
		}
	}

	return energy
}

// aksToMag2 is aks_to_mag2(): spectral amplitudes from the LPC filter with
// the LPC post-filter, and Aw, the spectrum of A(z) (bins 0..256).
func (d *Decoder) aksToMag2(ak *[lspOrd + 1]float32, mo *model, en float32, aw *[fftEnc/2 + 1]cpx) {
	var a [fftEnc]float32
	var a2 [fftEnc / 2]float32
	for i := 0; i <= lspOrd; i++ {
		a[i] = ak[i]
	}

	d.fwd.forward(&a, aw)
	for i := 0; i < fftEnc/2; i++ {
		a2[i] = float32(aw[i].r*aw[i].r) + float32(aw[i].i*aw[i].i) + 1e-6
	}

	// A(z/gamma)
	var ag [fftEnc]float32
	g := float32(0.5) // LPCPF_GAMMA
	ag[0] = ak[0]
	for i := 1; i <= lspOrd; i++ {
		ag[i] = float32(ak[i] * g)
		g = float32(float64(g) * 0.5)
	}

	var awg [fftEnc/2 + 1]cpx
	d.fwd.forward(&ag, &awg)

	var a2g [fftEnc / 2]float32
	for i := 0; i < fftEnc/2; i++ {
		a2g[i] = float32(awg[i].r*awg[i].r) + float32(awg[i].i*awg[i].i) + 1e-6
	}

	// normalisation gain
	var eBefore, eAfter float32 = 1e-12, 1e-12
	pw := &a2
	for i := 0; i < fftEnc/2; i++ {
		invA2 := 1.0 / a2[i]
		r := sqrtf(float32(a2g[i] * invA2))

		eBefore += invA2
		pw[i] = float32(invA2 * fastPowfPos(r+1e-5, lpcpfTwoBeta))
		eAfter += pw[i]
	}

	// boost low frequencies a bit (< 1000 Hz)
	for i := 0; i < fftEnc/2*1000/(sampRate/2); i++ {
		pw[i] *= 1.96
	}

	gain := float32(en*eBefore) / eAfter

	// Determine magnitudes
	for m := 1; m <= mo.l; m++ {
		ta := float32(float32(float32(m)-0.5) * mo.wo)
		tb := float32(float32(float32(m)+0.5) * mo.wo)
		am := int(float64(float64(ta)/fftR) + 0.5)
		bm := int(float64(float64(tb)/fftR) + 0.5)
		if bm > fftEnc/2 {
			bm = fftEnc / 2
		}

		var em float32
		for i := am; i < bm; i++ {
			em += pw[i]
		}

		mo.a[m] = sqrtf(float32(em * gain))
	}
}

// applyLPCCorrection is apply_lpc_correction(): the first harmonic
// correction for low pitched male speakers.
func applyLPCCorrection(mo *model) {
	if float64(mo.wo) < lpcCorrWo {
		mo.a[1] *= 0.032
	}
}
