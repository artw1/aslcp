package gsm

// Long-term prediction, GSM 06.10 sections 4.2.11-4.2.12 and 4.3.2.

// ltpParameters finds the lag Nc (40..120) and coded gain bc (0..3) that best
// predict the sub-segment d[0..39] from the reconstructed residual history.
// dp holds 120 history samples followed by the current position, so dp[120+k]
// is the sample at offset k; only dp[0..119] is read.
func ltpParameters(d []int16, dp []int16) (nc, bc int16) {
	var dmax int16
	for k := 0; k < 40; k++ {
		if t := abs16(d[k]); t > dmax {
			dmax = t
		}
	}
	var t int16
	if dmax != 0 {
		t = norm(int32(dmax) << 16)
	}
	var scal int16
	if t <= 6 {
		scal = 6 - t
	}

	var wt [40]int16
	for k := range wt {
		wt[k] = d[k] >> uint(scal)
	}

	// Search the cross-correlation maximum.
	var lMax int32
	nc = 40
	for lambda := 40; lambda <= 120; lambda++ {
		var sum int32
		base := 120 - lambda
		for k := 0; k < 40; k++ {
			sum += int32(wt[k]) * int32(dp[base+k])
		}
		if sum > lMax {
			nc = int16(lambda)
			lMax = sum
		}
	}
	lMax <<= 1
	lMax >>= uint(6 - scal)

	// Power of the selected history segment.
	var lPower int32
	base := 120 - int(nc)
	for k := 0; k < 40; k++ {
		v := int32(dp[base+k] >> 3)
		lPower += v * v
	}
	lPower <<= 1

	if lMax <= 0 {
		return nc, 0
	}
	if lMax >= lPower {
		return nc, 3
	}
	t = norm(lPower)
	r := int16((lMax << uint(t)) >> 16)
	s := int16((lPower << uint(t)) >> 16)
	for bc = 0; bc <= 2; bc++ {
		if r <= mult(s, ltpDLB[bc]) {
			break
		}
	}
	return nc, bc
}

// ltpFilter computes the prediction dpp and the residual e = d - dpp for one
// sub-segment (4.2.12). dp is laid out as for ltpParameters.
func ltpFilter(bc, nc int16, dp, d, dpp, e []int16) {
	bp := ltpQLB[bc]
	base := 120 - int(nc)
	for k := 0; k < 40; k++ {
		dpp[k] = multR(bp, dp[base+k])
		e[k] = sub(d[k], dpp[k])
	}
}

// ltpSynthesis is the decoder's long-term synthesis filter (4.3.2). drp holds
// 120 history samples followed by 40 output samples; on return drp[120..159]
// holds the new sub-segment and the history has been shifted by 40.
func (d *Decoder) ltpSynthesis(ncr, bcr int16, erp *[40]int16, out []int16) {
	nr := ncr
	if ncr < 40 || ncr > 120 {
		nr = d.nrp
	}
	d.nrp = nr
	brp := ltpQLB[bcr&3]
	drp := &d.drp
	for k := 0; k < 40; k++ {
		drpp := multR(brp, drp[120+k-int(nr)])
		drp[120+k] = add(erp[k], drpp)
	}
	copy(out, drp[120:160])
	copy(drp[0:120], drp[40:160])
}
