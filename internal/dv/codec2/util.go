// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) David Rowe and the other Codec2 contributors (Codec2,
// LGPL-2.1); Codec2-mod modifications by the M17 Project contributors.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) file(s)
// src/util.c, inc/util.h, src/interp.c at commit
// 7c334283794fa6304dc014f12df70afc8393143a and relicensed under LGPL-2.1
// section 3 to GPL-3.0-or-later. See doc.go for the full licence statement.

package codec2

import "math"

// fastAcosf is fast_acosf() from util.c.
func fastAcosf(x float32) float32 {
	if x < -1.0 {
		x = -1.0
	}
	if x > 1.0 {
		x = 1.0
	}
	neg := false
	if x < 0.0 {
		x = -x
		neg = true
	}
	// Horner evaluation; every product is rounded to float32 on its own
	// (explicit conversion) so that no x*y+z is fused into an FMA.
	p := float32(-0.004731162409361028*x) + 0.02013917916990988
	p = float32(p*x) - 0.04547268716508862
	p = float32(p*x) + 0.08798969877894365
	p = float32(p*x) - 0.2145148619010314
	p = float32(p*x) + 1.57079461300486
	y := float32(sqrtf(1.0-x) * p)
	if neg {
		return float32(piD) - y
	}
	return y
}

// fastCosf is fast_cosf() from util.c; the domain is [0, pi].
func fastCosf(x float32) float32 {
	sign := float32(1.0)
	if x > piHalf {
		x = float32(piD - float64(x))
		sign = -1.0
	}
	x2 := float32(x * x)
	p := float32(2.323747198784698e-5*x2) - 0.001385741491153336
	p = float32(p*x2) + 0.04166409053859771
	p = float32(p*x2) - 0.4999992686979083
	p = float32(p*x2) + 0.999999967262265
	return float32(sign * p)
}

// fastPowfPos is fast_powf_pos() from util.h: x^y for x > 0.
func fastPowfPos(x, y float32) float32 {
	u := math.Float32bits(x)
	e := float32(int(u>>23) - 127)
	u = (u & 0x007FFFFF) | 0x3F800000
	m := math.Float32frombits(u) - 1.0

	t := float32(m*0.04526550458) + -0.193510391
	t = float32(m*t) + 0.4152409602
	t = float32(m*t) + -0.708863876
	t = float32(m*t) + 1.441879776
	l2 := e + float32(m*t)

	tt := float32(y * l2)
	fl := floorf(tt)
	f := tt - fl
	q := float32(f*0.001895108641) + 0.008946224944
	q = float32(f*q) + 0.05586325839
	q = float32(f*q) + 0.2401407868
	q = float32(f*q) + 0.6931546159
	p := float32(0.999999896) + float32(f*q)
	r := math.Float32bits(p) + uint32(int32(fl)<<23)
	return math.Float32frombits(r)
}

// interpWo is interp_Wo(): Wo and L for the 10 ms frame between prev and
// next.
func interpWo(interp *model, prev, next *model) {
	// trap corner case where voicing est is probably wrong
	if interp.voiced && !prev.voiced && !next.voiced {
		interp.voiced = false
	}
	if interp.voiced {
		if prev.voiced && next.voiced {
			interp.wo = float32(0.5*float64(prev.wo) + 0.5*float64(next.wo))
		}
		if !prev.voiced && next.voiced {
			interp.wo = next.wo
		}
		if prev.voiced && !next.voiced {
			interp.wo = prev.wo
		}
	} else {
		interp.wo = woMinF
	}
	interp.l = int(cPi / float64(interp.wo))
}

// interpEnergy is interp_energy().
func interpEnergy(prevE, nextE float32) float32 {
	return sqrtf(float32(prevE * nextE))
}

// interpolateLSP is interpolate_lsp().
func interpolateLSP(interp, prev, next *[lspOrd]float32) {
	for i := 0; i < lspOrd; i++ {
		interp[i] = float32(0.5*float64(prev[i]) + 0.5*float64(next[i]))
	}
}

// powf is powf(x, y), evaluated in double and rounded once.
func powf(x, y float32) float32 { return float32(math.Pow(float64(x), float64(y))) }
