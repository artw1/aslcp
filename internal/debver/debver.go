// Package debver compares Debian package versions with the dpkg algorithm
// ([epoch:]upstream[-revision]), without calling dpkg.
package debver

import (
	"strconv"
	"strings"
)

// Version is a parsed Debian version.
type Version struct {
	Epoch    int
	Upstream string
	Revision string
}

// Parse splits v into epoch, upstream version and revision. It does not
// validate characters beyond what is needed to split; use Valid for that.
func Parse(v string) Version {
	v = strings.TrimSpace(v)
	var out Version
	if i := strings.Index(v, ":"); i >= 0 {
		if e, err := strconv.Atoi(v[:i]); err == nil && e >= 0 {
			out.Epoch = e
			v = v[i+1:]
		}
	}
	if i := strings.LastIndex(v, "-"); i >= 0 {
		out.Revision = v[i+1:]
		v = v[:i]
	}
	out.Upstream = v
	return out
}

// Valid reports whether v is a well-formed Debian version: an optional
// numeric epoch, an upstream part starting with a digit made of
// [A-Za-z0-9.+~-:] (hyphen only with a revision), and a revision made of
// [A-Za-z0-9.+~].
func Valid(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return false
	}
	if i := strings.Index(v, ":"); i >= 0 {
		if i == 0 {
			return false
		}
		if _, err := strconv.ParseUint(v[:i], 10, 31); err != nil {
			return false
		}
		v = v[i+1:]
	}
	p := Parse(v)
	if p.Upstream == "" || p.Upstream[0] < '0' || p.Upstream[0] > '9' {
		return false
	}
	if strings.Contains(v, "-") && p.Revision == "" {
		return false
	}
	for _, r := range p.Upstream {
		if !isAlnum(r) && !strings.ContainsRune(".+~-", r) {
			return false
		}
	}
	for _, r := range p.Revision {
		if !isAlnum(r) && !strings.ContainsRune(".+~", r) {
			return false
		}
	}
	return true
}

func isAlnum(r rune) bool {
	return (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// Compare returns -1, 0 or 1 as a is older than, equal to or newer than b.
func Compare(a, b string) int {
	pa, pb := Parse(a), Parse(b)
	if pa.Epoch != pb.Epoch {
		if pa.Epoch < pb.Epoch {
			return -1
		}
		return 1
	}
	if c := compareString(pa.Upstream, pb.Upstream); c != 0 {
		return c
	}
	return compareString(pa.Revision, pb.Revision)
}

// order is dpkg's character weight: '~' sorts before everything (even the
// end of the string), digits are handled separately, letters sort before
// other characters.
func order(c byte) int {
	switch {
	case c == '~':
		return -1
	case c >= '0' && c <= '9':
		return 0
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
		return int(c)
	}
	return int(c) + 256
}

// compareString is dpkg's verrevcmp.
func compareString(a, b string) int {
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		firstDiff := 0
		for (i < len(a) && !isDigit(a[i])) || (j < len(b) && !isDigit(b[j])) {
			ac, bc := 0, 0
			if i < len(a) {
				ac = order(a[i])
			}
			if j < len(b) {
				bc = order(b[j])
			}
			if ac != bc {
				if ac < bc {
					return -1
				}
				return 1
			}
			i++
			j++
		}
		for i < len(a) && a[i] == '0' {
			i++
		}
		for j < len(b) && b[j] == '0' {
			j++
		}
		for i < len(a) && isDigit(a[i]) && j < len(b) && isDigit(b[j]) {
			if firstDiff == 0 {
				firstDiff = int(a[i]) - int(b[j])
			}
			i++
			j++
		}
		if i < len(a) && isDigit(a[i]) {
			return 1
		}
		if j < len(b) && isDigit(b[j]) {
			return -1
		}
		if firstDiff != 0 {
			if firstDiff < 0 {
				return -1
			}
			return 1
		}
	}
	return 0
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }
