package nxdn

// Rate 1/2, constraint length 5 convolutional code of NXDN
// (G1 = 1+D^3+D^4, G2 = 1+D+D^2+D^4) with a soft-decision Viterbi decoder.
// Port of MMDVMHost NXDNConvolution.cpp.

var (
	branchTable1 = [8]int{0, 0, 0, 0, 2, 2, 2, 2}
	branchTable2 = [8]int{0, 2, 2, 0, 0, 2, 2, 0}
)

const (
	convStates   = 16
	convStatesD2 = 8
	convM        = 4
	convK        = 5
)

// ConvEncode encodes the first nbits of in; the result has 2*nbits bits
// (packed MSB first).
func ConvEncode(in []byte, nbits int) []byte {
	out := make([]byte, (2*nbits+7)/8)
	var d1, d2, d3, d4 uint8
	k := 0
	for i := 0; i < nbits; i++ {
		var d uint8
		if getBit(in, i) {
			d = 1
		}
		g1 := (d + d3 + d4) & 1
		g2 := (d + d1 + d2 + d4) & 1
		d4, d3, d2, d1 = d3, d2, d1, d
		setBit(out, k, g1 != 0)
		k++
		setBit(out, k, g2 != 0)
		k++
	}
	return out
}

// Viterbi is the decoder state. Symbols are soft: 0 = strong zero, 1 =
// erasure (punctured), 2 = strong one.
type Viterbi struct {
	old, new  [convStates]uint16
	decisions []uint64
}

// Start resets the decoder.
func (v *Viterbi) Start() {
	v.old = [convStates]uint16{}
	v.new = [convStates]uint16{}
	v.decisions = v.decisions[:0]
}

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// Decode feeds one pair of symbols.
func (v *Viterbi) Decode(s0, s1 uint8) {
	var dec uint64
	for i := 0; i < convStatesD2; i++ {
		j := i * 2
		metric := uint16(absInt(branchTable1[i]-int(s0)) + absInt(branchTable2[i]-int(s1)))

		m0 := v.old[i] + metric
		m1 := v.old[i+convStatesD2] + (convM - metric)
		var d0 uint64
		if m0 >= m1 {
			d0 = 1
			v.new[j] = m1
		} else {
			v.new[j] = m0
		}

		m0 = v.old[i] + (convM - metric)
		m1 = v.old[i+convStatesD2] + metric
		var d1 uint64
		if m0 >= m1 {
			d1 = 1
			v.new[j+1] = m1
		} else {
			v.new[j+1] = m0
		}
		dec |= d1<<uint(j+1) | d0<<uint(j)
	}
	v.decisions = append(v.decisions, dec)
	v.old, v.new = v.new, v.old
}

// Chainback writes the last nbits decoded bits into out (MSB first) and
// returns the estimated number of corrected channel errors.
func (v *Viterbi) Chainback(out []byte, nbits int) int {
	state := uint32(0)
	dp := len(v.decisions)
	for n := nbits; n > 0; {
		n--
		dp--
		i := state >> (9 - convK)
		bit := uint8(v.decisions[dp]>>i) & 1
		state = uint32(bit)<<7 | state>>1
		setBit(out, n, bit != 0)
	}
	minCost := v.old[0]
	for _, m := range v.old {
		if m < minCost {
			minCost = m
		}
	}
	return int(minCost) / (convM >> 1)
}
