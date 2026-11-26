package gsm

// LPC analysis of the encoder, GSM 06.10 sections 4.2.0 to 4.2.7.

// preprocess runs the downscaling, offset compensation and pre-emphasis of
// section 4.2.0-4.2.3 on one frame of input s, writing the result to so.
func (e *Encoder) preprocess(s *[FrameSamples]int16, so *[FrameSamples]int16) {
	z1 := e.z1
	lz2 := e.lz2
	mp := e.mp
	for k := 0; k < FrameSamples; k++ {
		// Downscale by 1/2 with the three low bits cleared.
		so0 := (s[k] >> 3) << 2

		// Offset compensation: a high-pass filter. The difference cannot
		// overflow because so0 is at most half scale.
		s1 := so0 - z1
		z1 = so0
		ls2 := int32(s1) << 15
		msp := int16(lz2 >> 15)
		lsp := int16(lz2 - int32(msp)<<15)
		ls2 += int32(multR(lsp, 32735))
		lz2 = lAdd(int32(msp)*32735, ls2)

		// Round, then pre-emphasis.
		ltemp := lAdd(lz2, 16384)
		msp = multR(mp, -28180)
		mp = int16(ltemp >> 15)
		so[k] = add(mp, msp)
	}
	e.z1 = z1
	e.lz2 = lz2
	e.mp = mp
}

// autocorrelation computes the first nine autocorrelation values of s
// (section 4.2.4). As in the reference, s is scaled down in place to avoid
// overflow and then scaled back up, which drops low bits that the later
// stages see.
func autocorrelation(s *[FrameSamples]int16, lACF *[9]int32) {
	var smax int16
	for _, v := range s {
		if t := abs16(v); t > smax {
			smax = t
		}
	}
	var scalauto int16
	if smax != 0 {
		scalauto = 4 - norm(int32(smax)<<16)
	}
	if scalauto > 0 {
		f := int16(16384 >> uint(scalauto-1))
		for k := range s {
			s[k] = multR(s[k], f)
		}
	}
	for k := 0; k < 9; k++ {
		var sum int32
		for i := k; i < FrameSamples; i++ {
			sum += int32(s[i]) * int32(s[i-k])
		}
		lACF[k] = sum << 1
	}
	if scalauto > 0 {
		for k := range s {
			s[k] <<= uint(scalauto)
		}
	}
}

// reflectionCoefficients derives r[0..7] from the autocorrelation with the
// Schur recursion (section 4.2.5).
func reflectionCoefficients(lACF *[9]int32, r *[8]int16) {
	if lACF[0] == 0 {
		*r = [8]int16{}
		return
	}
	var acf, p, kk [9]int16
	t := norm(lACF[0])
	for i := range acf {
		acf[i] = int16((lACF[i] << uint(t)) >> 16)
	}
	for i := 1; i <= 7; i++ {
		kk[i] = acf[i]
	}
	p = acf
	for n := 1; n <= 8; n++ {
		temp := abs16(p[1])
		if p[0] < temp {
			for i := n - 1; i < 8; i++ {
				r[i] = 0
			}
			return
		}
		rn := div(temp, p[0])
		if p[1] > 0 {
			rn = -rn
		}
		r[n-1] = rn
		if n == 8 {
			return
		}
		p[0] = add(p[0], multR(p[1], rn))
		for m := 1; m <= 8-n; m++ {
			p[m] = add(p[m+1], multR(kk[m], rn))
			kk[m] = add(kk[m], multR(p[m+1], rn))
		}
	}
}

// toLogAreaRatios replaces the reflection coefficients with the piecewise
// linear log-area-ratio approximation of section 4.2.6.
func toLogAreaRatios(r *[8]int16) {
	for i, v := range r {
		t := abs16(v)
		switch {
		case t < 22118:
			t >>= 1
		case t < 31130:
			t -= 11059
		default:
			t = (t - 26112) << 2
		}
		if v < 0 {
			t = -t
		}
		r[i] = t
	}
}

// quantizeLARs codes the log-area ratios into LARc (section 4.2.7).
func quantizeLARs(lar *[8]int16, larc *[8]int16) {
	for i, st := range larStep {
		t := mult(st.a, lar[i])
		t = add(t, st.b)
		t = add(t, 256)
		t >>= 9
		switch {
		case t > st.mac:
			larc[i] = st.mac - st.mic
		case t < st.mic:
			larc[i] = 0
		default:
			larc[i] = t - st.mic
		}
	}
}
