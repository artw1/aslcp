package gsm

// Regular pulse excitation coding, GSM 06.10 sections 4.2.13-4.2.18 and
// 4.3.1.

// weightingFilter applies the RPE weighting filter to e, which holds five
// zero samples, the 40 residual samples and five more zeros (4.2.13).
func weightingFilter(e *[50]int16, x *[40]int16) {
	for k := 0; k < 40; k++ {
		var acc int32 = 4096 // rounding
		for i := 0; i < 11; i++ {
			acc += int32(e[k+i]) * int32(weightH[i])
		}
		x[k] = sat(acc >> 13)
	}
}

// gridSelection picks the decimation phase Mc with the most energy and
// returns its 13 samples in xM (4.2.14).
func gridSelection(x *[40]int16, xM *[13]int16) (mc int16) {
	var em int32
	for m := 0; m < 4; m++ {
		var sum int32
		for i := 0; i < 13; i++ {
			v := int32(x[m+3*i] >> 2)
			sum += v * v
		}
		sum <<= 1
		if sum > em {
			mc = int16(m)
			em = sum
		}
	}
	for i := 0; i < 13; i++ {
		xM[i] = x[int(mc)+3*i]
	}
	return mc
}

// expMant splits the coded block maximum into exponent and mantissa
// (4.2.15).
func expMant(xmaxc int16) (exp, mant int16) {
	if xmaxc > 15 {
		exp = (xmaxc >> 3) - 1
	}
	mant = xmaxc - exp<<3
	if mant == 0 {
		return -4, 7
	}
	for mant <= 7 {
		mant = mant<<1 | 1
		exp--
	}
	return exp, mant - 8
}

// apcmQuantize codes the block maximum and the 13 pulses (4.2.15).
func apcmQuantize(xM *[13]int16, xMc *[13]int16) (xmaxc, exp, mant int16) {
	var xmax int16
	for _, v := range xM {
		if t := abs16(v); t > xmax {
			xmax = t
		}
	}
	t := xmax >> 9
	itest := false
	for i := 0; i <= 5; i++ {
		itest = itest || t <= 0
		t >>= 1
		if !itest {
			exp++
		}
	}
	xmaxc = add(xmax>>uint(exp+5), exp<<3)

	exp, mant = expMant(xmaxc)
	t1 := uint(6 - exp)
	t2 := rpeNRFAC[mant]
	for i, v := range xM {
		q := v << t1 // cannot overflow by construction of exp
		q = mult(q, t2)
		xMc[i] = q>>12 + 4
	}
	return xmaxc, exp, mant
}

// apcmDequantize reconstructs the 13 pulses (4.2.16).
func apcmDequantize(xMc *[13]int16, exp, mant int16, xMp *[13]int16) {
	t1 := rpeFAC[mant]
	t2 := sub(6, exp)
	t3 := asl(1, sub(t2, 1))
	for i, c := range xMc {
		v := (c<<1 - 7) << 12 // 3-bit code to a signed Q12 value
		v = multR(t1, v)
		v = add(v, t3)
		xMp[i] = asr(v, t2)
	}
}

// gridPosition places the pulses at phase mc in a 40-sample block (4.2.17).
func gridPosition(mc int16, xMp *[13]int16, ep []int16) {
	for k := range ep[:40] {
		ep[k] = 0
	}
	for i, v := range xMp {
		ep[int(mc&3)+3*i] = v
	}
}
