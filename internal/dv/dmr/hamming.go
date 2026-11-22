package dmr

// Hamming codes used by BPTC(196,96) (15,11,3 rows and 13,9,3 columns) and the
// embedded LC (16,11,4 rows). Parity equations from ETSI TS 102 361-1 B.3,
// as written in MMDVMHost Hamming.cpp (G4KLX, GPL-2.0-or-later).

type hamming struct {
	k, n   int
	parity [][]int // data bit indices of each parity bit
	syn    map[uint32]int
}

func newHamming(k int, parity [][]int) *hamming {
	h := &hamming{k: k, n: k + len(parity), parity: parity, syn: map[uint32]int{}}
	for i := 0; i < h.n; i++ {
		var s uint32
		if i < k {
			for j, p := range parity {
				for _, d := range p {
					if d == i {
						s |= 1 << j
					}
				}
			}
		} else {
			s = 1 << (i - k)
		}
		h.syn[s] = i
	}
	return h
}

var (
	hamming15113 = newHamming(11, [][]int{
		{0, 1, 2, 3, 5, 7, 8}, {1, 2, 3, 4, 6, 8, 9}, {2, 3, 4, 5, 7, 9, 10}, {0, 1, 2, 4, 6, 7, 10}})
	hamming1393 = newHamming(9, [][]int{
		{0, 1, 3, 5, 6}, {0, 1, 2, 4, 6, 7}, {0, 1, 2, 3, 5, 7, 8}, {0, 2, 4, 5, 8}})
	hamming16114 = newHamming(11, [][]int{
		{0, 1, 2, 3, 5, 7, 8}, {1, 2, 3, 4, 6, 8, 9}, {2, 3, 4, 5, 7, 9, 10}, {0, 1, 2, 4, 6, 7, 10}, {0, 2, 5, 6, 8, 9, 10}})
)

// encode fills the parity bits d[k:n] from d[0:k].
func (h *hamming) encode(d []bool) {
	for j, p := range h.parity {
		v := false
		for _, i := range p {
			v = v != d[i]
		}
		d[h.k+j] = v
	}
}

func (h *hamming) syndrome(d []bool) uint32 {
	var s uint32
	for j, p := range h.parity {
		v := d[h.k+j]
		for _, i := range p {
			v = v != d[i]
		}
		if v {
			s |= 1 << j
		}
	}
	return s
}

// decode corrects a single error in d. It reports whether a bit was changed
// and whether the word is (now) valid.
func (h *hamming) decode(d []bool) (fixed, ok bool) {
	s := h.syndrome(d)
	if s == 0 {
		return false, true
	}
	i, found := h.syn[s]
	if !found {
		return false, false
	}
	d[i] = !d[i]
	return true, true
}
