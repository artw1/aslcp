package configsvc

import (
	"fmt"
	"strings"
)

// dline is one line of a text; noEOL marks a final line without newline.
type dline struct {
	text  string
	noEOL bool
}

func splitLines(s string) []dline {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, "\n")
	out := make([]dline, 0, len(parts))
	for i, p := range parts {
		if i == len(parts)-1 {
			if p != "" {
				out = append(out, dline{p, true})
			}
			break
		}
		out = append(out, dline{text: p})
	}
	return out
}

type opKind byte

const (
	opEq  opKind = ' '
	opDel opKind = '-'
	opIns opKind = '+'
)

type op struct {
	kind opKind
	line dline
}

// maxEditDistance bounds the Myers search (memory is O(D^2)); beyond it the
// differing middle is reported as one replaced block.
const maxEditDistance = 3000

// diffLines returns the edit script turning a into b (Myers O(ND)).
func diffLines(a, b []dline) []op {
	// common prefix and suffix
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	var out []op
	for _, l := range a[:pre] {
		out = append(out, op{opEq, l})
	}
	out = append(out, myers(a[pre:len(a)-suf], b[pre:len(b)-suf])...)
	for _, l := range a[len(a)-suf:] {
		out = append(out, op{opEq, l})
	}
	return out
}

func replaceAll(a, b []dline) []op {
	out := make([]op, 0, len(a)+len(b))
	for _, l := range a {
		out = append(out, op{opDel, l})
	}
	for _, l := range b {
		out = append(out, op{opIns, l})
	}
	return out
}

func myers(a, b []dline) []op {
	n, m := len(a), len(b)
	if n == 0 || m == 0 {
		return replaceAll(a, b)
	}
	max := n + m
	if max > maxEditDistance {
		max = maxEditDistance
	}
	off := max
	v := make([]int, 2*max+2)
	var trace [][]int
	found := -1
outer:
	for d := 0; d <= max; d++ {
		snap := make([]int, 2*d+1)
		for k := -d; k <= d; k += 2 {
			var x int
			if k == -d || (k != d && v[off+k-1] < v[off+k+1]) {
				x = v[off+k+1]
			} else {
				x = v[off+k-1] + 1
			}
			y := x - k
			for x < n && y < m && a[x] == b[y] {
				x++
				y++
			}
			v[off+k] = x
			if x >= n && y >= m {
				found = d
				// record state for this d too
				for kk := -d; kk <= d; kk += 2 {
					snap[kk+d] = v[off+kk]
				}
				trace = append(trace, snap)
				break outer
			}
		}
		for kk := -d; kk <= d; kk += 2 {
			snap[kk+d] = v[off+kk]
		}
		trace = append(trace, snap)
	}
	if found < 0 {
		return replaceAll(a, b)
	}
	// backtrack
	var rev []op
	x, y := n, m
	for d := found; d > 0; d-- {
		prev := trace[d-1] // v after round d-1, indexed k+(d-1)
		get := func(k int) int { return prev[k+d-1] }
		k := x - y
		var pk int
		if k == -d || (k != d && get(k-1) < get(k+1)) {
			pk = k + 1
		} else {
			pk = k - 1
		}
		px := get(pk)
		py := px - pk
		for x > px && y > py {
			x--
			y--
			rev = append(rev, op{opEq, a[x]})
		}
		if x == px {
			y--
			rev = append(rev, op{opIns, b[y]})
		} else {
			x--
			rev = append(rev, op{opDel, a[x]})
		}
	}
	for x > 0 && y > 0 {
		x--
		y--
		rev = append(rev, op{opEq, a[x]})
	}
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	return rev
}

const diffContext = 3

// UnifiedDiff returns a unified diff (3 lines of context) of oldText to
// newText, with "a/<path>" and "b/<path>" headers ("/dev/null" for a side
// that is oldMissing / newMissing). Equal texts give "".
func UnifiedDiff(path, oldText, newText string, oldMissing bool) string {
	if oldText == newText && !oldMissing {
		return ""
	}
	ops := diffLines(splitLines(oldText), splitLines(newText))
	changed := false
	for _, o := range ops {
		if o.kind != opEq {
			changed = true
			break
		}
	}
	if !changed && !oldMissing {
		return ""
	}
	var sb strings.Builder
	from := "a/" + path
	if oldMissing {
		from = "/dev/null"
	}
	fmt.Fprintf(&sb, "--- %s\n+++ b/%s\n", from, path)

	// hunks
	type span struct{ start, end int } // indexes into ops, end exclusive
	var spans []span
	for i := 0; i < len(ops); i++ {
		if ops[i].kind == opEq {
			continue
		}
		s := i - diffContext
		if s < 0 {
			s = 0
		}
		j := i
		for j < len(ops) && ops[j].kind != opEq {
			j++
		}
		// extend over short equal runs
		for {
			k := j
			for k < len(ops) && ops[k].kind == opEq {
				k++
			}
			if k < len(ops) && k-j <= 2*diffContext {
				for k < len(ops) && ops[k].kind != opEq {
					k++
				}
				j = k
				continue
			}
			break
		}
		e := j + diffContext
		if e > len(ops) {
			e = len(ops)
		}
		if len(spans) > 0 && s <= spans[len(spans)-1].end {
			spans[len(spans)-1].end = e
		} else {
			spans = append(spans, span{s, e})
		}
		i = j
	}
	// line numbers before each op
	oldNo, newNo := make([]int, len(ops)+1), make([]int, len(ops)+1)
	o, n := 1, 1
	for i, p := range ops {
		oldNo[i], newNo[i] = o, n
		if p.kind != opIns {
			o++
		}
		if p.kind != opDel {
			n++
		}
	}
	for _, sp := range spans {
		oc, nc := 0, 0
		for _, p := range ops[sp.start:sp.end] {
			if p.kind != opIns {
				oc++
			}
			if p.kind != opDel {
				nc++
			}
		}
		os, ns := oldNo[sp.start], newNo[sp.start]
		if oc == 0 {
			os--
		}
		if nc == 0 {
			ns--
		}
		fmt.Fprintf(&sb, "@@ -%s +%s @@\n", rng(os, oc), rng(ns, nc))
		for _, p := range ops[sp.start:sp.end] {
			sb.WriteByte(byte(p.kind))
			sb.WriteString(p.line.text)
			sb.WriteByte('\n')
			if p.line.noEOL {
				sb.WriteString("\\ No newline at end of file\n")
			}
		}
	}
	return sb.String()
}

func rng(start, count int) string {
	if count == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}
