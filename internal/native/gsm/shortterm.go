package gsm

// Short-term analysis and synthesis filtering, GSM 06.10 sections 4.2.8-4.2.10
// and 4.3.3-4.3.4. The coded log-area ratios are decoded, interpolated with
// the previous frame's values over four segments of the frame, turned back
// into reflection coefficients and applied with a lattice filter.

// shortTerm holds the state shared by the analysis and synthesis paths.
type shortTerm struct {
	prevLARpp [8]int16 // decoded LARs of the previous frame
	u         [8]int16 // analysis lattice memory
	v         [9]int16 // synthesis lattice memory
}

// segments are the sample ranges over which one set of interpolated
// coefficients is used.
var segments = [4]struct{ start, n int }{{0, 13}, {13, 14}, {27, 13}, {40, 120}}

// decodeLARs turns coded log-area ratios back into LAR values (4.2.8).
func decodeLARs(larc *[8]int16, larpp *[8]int16) {
	for i := range larc {
		t1 := add(larc[i], larMIC[i]) << 10
		t2 := larB[i] << 1
		t1 = sub(t1, t2)
		t1 = multR(larINVA[i], t1)
		larpp[i] = add(t1, t1)
	}
}

// interpolate computes the LARs for segment seg from the previous (p) and
// current (c) frame values (4.2.9.1).
func interpolate(seg int, p, c *[8]int16, larp *[8]int16) {
	for i := range larp {
		switch seg {
		case 0:
			t := add(p[i]>>2, c[i]>>2)
			larp[i] = add(t, p[i]>>1)
		case 1:
			larp[i] = add(p[i]>>1, c[i]>>1)
		case 2:
			t := add(p[i]>>2, c[i]>>2)
			larp[i] = add(t, c[i]>>1)
		default:
			larp[i] = c[i]
		}
	}
}

// larToRP converts interpolated LARs back to reflection coefficients in
// place (4.2.9.2).
func larToRP(larp *[8]int16) {
	for i, v := range larp {
		var t int16
		if v < 0 {
			if v == minWord {
				t = maxWord
			} else {
				t = -v
			}
		} else {
			t = v
		}
		switch {
		case t < 11059:
			t <<= 1
		case t < 20070:
			t += 11059
		default:
			t = add(t>>2, 26112)
		}
		if v < 0 {
			t = -t
		}
		larp[i] = t
	}
}

// coefficients yields the reflection coefficients for each segment of the
// frame whose coded LARs are larc, then remembers them for the next frame.
func (st *shortTerm) coefficients(larc *[8]int16, fn func(rp *[8]int16, start, n int)) {
	var cur, rp [8]int16
	decodeLARs(larc, &cur)
	for seg, r := range segments {
		interpolate(seg, &st.prevLARpp, &cur, &rp)
		larToRP(&rp)
		fn(&rp, r.start, r.n)
	}
	st.prevLARpp = cur
}

// analysis filters s in place into the short-term residual d (4.2.10).
func (st *shortTerm) analysis(larc *[8]int16, s *[FrameSamples]int16) {
	st.coefficients(larc, func(rp *[8]int16, start, n int) {
		u := &st.u
		for k := start; k < start+n; k++ {
			di := s[k]
			sav := di
			for i := 0; i < 8; i++ {
				ui := u[i]
				rpi := rp[i]
				u[i] = sav
				sav = add(ui, multR(rpi, di))
				di = add(di, multR(rpi, ui))
			}
			s[k] = di
		}
	})
}

// synthesis filters the reconstructed residual wt into speech sr (4.3.4).
func (st *shortTerm) synthesis(larc *[8]int16, wt, sr *[FrameSamples]int16) {
	st.coefficients(larc, func(rrp *[8]int16, start, n int) {
		v := &st.v
		for k := start; k < start+n; k++ {
			sri := wt[k]
			for i := 7; i >= 0; i-- {
				sri = sub(sri, multR(rrp[i], v[i]))
				v[i+1] = add(v[i], multR(rrp[i], sri))
			}
			v[0] = sri
			sr[k] = sri
		}
	})
}
