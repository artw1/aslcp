// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (c) 2003-2010, Mark Borgerding. All rights reserved.
// This file is part of KISS FFT (https://github.com/mborgerding/kissfft),
// SPDX-License-Identifier: BSD-3-Clause.
//
// Derived from Codec2-mod (https://github.com/M17-Project/Codec2-mod) files
// src/kiss_fft.c, src/kiss_fftr.c, inc/_kiss_fft_guts.h at commit
// 7c334283794fa6304dc014f12df70afc8393143a. The BSD-3-Clause notice of the
// original applies to the parts derived from KISS FFT and is reproduced in
// packaging/THIRD-PARTY-NOTICES; the combined work is distributed under
// GPL-3.0-or-later (BSD-3-Clause is GPL-compatible). Codec2-mod's copy is
// used here under LGPL-2.1 section 3 as GPL-3.0-or-later; see doc.go.

package codec2

import "math"

// cpx is kiss_fft_cpx with float32 scalars.
type cpx struct{ r, i float32 }

// A 512-point real FFT is a 256-point complex FFT plus a split step.
// 256 = 4*4*4*4, so kf_factor() yields (4,64),(4,16),(4,4),(4,1) and only
// the radix-4 butterfly of the original is ever executed; the other radices
// are not ported.
const (
	fftN   = fftEnc / 2 // complex FFT size
	fftRad = 4
)

// fftPlan is kiss_fft_state for nfft = 256.
type fftPlan struct {
	inverse bool
	tw      [fftN]cpx
}

func (p *fftPlan) init(inverse bool) {
	p.inverse = inverse
	for i := 0; i < fftN; i++ {
		c, sn := math.Float32frombits(kissTwiddles[i][0]), math.Float32frombits(kissTwiddles[i][1])
		if inverse {
			sn = -sn
		}
		p.tw[i] = cpx{c, sn}
	}
}

// work is kf_work() specialised to radix 4 with in_stride 1.
func (p *fftPlan) work(fout, f []cpx, fstride, level int) {
	m := fftN >> (2 * uint(level+1)) // 64, 16, 4, 1
	if m == 1 {
		for i := 0; i < fftRad; i++ {
			fout[i] = f[i*fstride]
		}
	} else {
		for i := 0; i < fftRad; i++ {
			p.work(fout[i*m:], f[i*fstride:], fstride*fftRad, level+1)
		}
	}
	p.bfly4(fout, fstride, m)
}

// cmul is C_MUL with the products rounded separately (no FMA fusion).
func cmul(a, b cpx) cpx {
	return cpx{
		float32(a.r*b.r) - float32(a.i*b.i),
		float32(a.r*b.i) + float32(a.i*b.r),
	}
}

func (p *fftPlan) bfly4(fout []cpx, fstride, m int) {
	m2, m3 := 2*m, 3*m
	for k := 0; k < m; k++ {
		s0 := cmul(fout[k+m], p.tw[k*fstride])
		s1 := cmul(fout[k+m2], p.tw[2*k*fstride])
		s2 := cmul(fout[k+m3], p.tw[3*k*fstride])

		f := fout[k]
		s5 := cpx{f.r - s1.r, f.i - s1.i}
		f = cpx{f.r + s1.r, f.i + s1.i}
		s3 := cpx{s0.r + s2.r, s0.i + s2.i}
		s4 := cpx{s0.r - s2.r, s0.i - s2.i}
		fout[k+m2] = cpx{f.r - s3.r, f.i - s3.i}
		fout[k] = cpx{f.r + s3.r, f.i + s3.i}

		if p.inverse {
			fout[k+m] = cpx{s5.r - s4.i, s5.i + s4.r}
			fout[k+m3] = cpx{s5.r + s4.i, s5.i - s4.r}
		} else {
			fout[k+m] = cpx{s5.r + s4.i, s5.i - s4.r}
			fout[k+m3] = cpx{s5.r - s4.i, s5.i + s4.r}
		}
	}
}

// rfft is kiss_fftr_state for nfft = 512.
type rfft struct {
	sub   fftPlan
	tmp   [fftN]cpx
	in    [fftN]cpx
	super [fftN / 2]cpx
}

func (s *rfft) init(inverse bool) {
	s.sub.init(inverse)
	for i := 0; i < fftN/2; i++ {
		c, sn := math.Float32frombits(kissSuperTwiddles[i][0]), math.Float32frombits(kissSuperTwiddles[i][1])
		if inverse {
			sn = -sn
		}
		s.super[i] = cpx{c, sn}
	}
}

// forward is kiss_fftr: 512 real samples in, bins 0..256 out.
func (s *rfft) forward(in *[fftEnc]float32, out *[fftEnc/2 + 1]cpx) {
	for k := 0; k < fftN; k++ {
		s.in[k] = cpx{in[2*k], in[2*k+1]}
	}
	s.sub.work(s.tmp[:], s.in[:], 1, 0)

	tdc := s.tmp[0]
	out[0] = cpx{tdc.r + tdc.i, 0}
	out[fftN] = cpx{tdc.r - tdc.i, 0}

	for k := 1; k <= fftN/2; k++ {
		fpk := s.tmp[k]
		fpnk := cpx{s.tmp[fftN-k].r, -s.tmp[fftN-k].i}
		f1k := cpx{fpk.r + fpnk.r, fpk.i + fpnk.i}
		f2k := cpx{fpk.r - fpnk.r, fpk.i - fpnk.i}
		tw := cmul(f2k, s.super[k-1])
		out[k] = cpx{(f1k.r + tw.r) * 0.5, (f1k.i + tw.i) * 0.5}
		out[fftN-k] = cpx{(f1k.r - tw.r) * 0.5, (tw.i - f1k.i) * 0.5}
	}
}

// inverse is kiss_fftri: bins 0..256 in, 512 real samples out.
func (s *rfft) inverse(in *[fftEnc/2 + 1]cpx, out *[fftEnc]float32) {
	s.tmp[0] = cpx{in[0].r + in[fftN].r, in[0].r - in[fftN].r}
	for k := 1; k <= fftN/2; k++ {
		fk := in[k]
		fnkc := cpx{in[fftN-k].r, -in[fftN-k].i}
		fek := cpx{fk.r + fnkc.r, fk.i + fnkc.i}
		t := cpx{fk.r - fnkc.r, fk.i - fnkc.i}
		fok := cmul(t, s.super[k-1])
		s.tmp[k] = cpx{fek.r + fok.r, fek.i + fok.i}
		s.tmp[fftN-k] = cpx{fek.r - fok.r, -(fek.i - fok.i)}
	}
	s.sub.work(s.in[:], s.tmp[:], 1, 0)
	for k := 0; k < fftN; k++ {
		out[2*k] = s.in[k].r
		out[2*k+1] = s.in[k].i
	}
}
