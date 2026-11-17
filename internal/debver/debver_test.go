package debver

import "testing"

func TestCompare(t *testing.T) {
	// each row: a < b
	less := [][2]string{
		{"0.2.1", "0.2.10"},
		{"0.2.9", "0.2.10"},
		{"0.2.1", "0.2.1-1"},
		{"0.2.1-1", "0.2.1-2"},
		{"0.2.1-2", "0.2.1-10"},
		{"1.0~rc1", "1.0"},
		{"1.0~rc1", "1.0~rc2"},
		{"1.0~~", "1.0~"},
		{"1.0~", "1.0"},
		{"1.0~rc1", "1.0~rc1-1"},
		{"0.0.0~git1a2b3c4", "0.1.0"},
		{"0.0.0~git1a2b3c4", "0.0.0"},
		{"0.1.2", "1:0.0.1"},
		{"1:1.0", "2:0.1"},
		{"0:9", "1:0"},
		{"1.0", "1.0+b1"},
		{"1.0", "1.0a"},
		{"1.0a", "1.0+"},
		{"1.0+", "1.0."},
		{"1.9", "1.10"},
		{"1.0", "1.0.1"},
		{"2.7.4", "2.7.4+dfsg"},
		{"1.0-1~bpo1", "1.0-1"},
		{"1.0-0.1", "1.0-1"},
		{"0.2.0", "0.2.1"},
		{"0.9.9", "0.10.0"},
		{"a", "b"},
		{"1.0A", "1.0a"},
	}
	for _, c := range less {
		if got := Compare(c[0], c[1]); got != -1 {
			t.Errorf("Compare(%q,%q) = %d, want -1", c[0], c[1], got)
		}
		if got := Compare(c[1], c[0]); got != 1 {
			t.Errorf("Compare(%q,%q) = %d, want 1", c[1], c[0], got)
		}
	}
	equal := [][2]string{
		{"1.0", "1.0"},
		{"0:1.0", "1.0"},
		{"1.0", "1.00"},
		{"1.0-0", "1.0"},
		{"1.0-1", "1.0-01"},
		{"001", "1"},
		{"0.2.1", "0.2.1"},
		{"0.0.0~git1", "0.0.0~git1"},
		{" 1.0 ", "1.0"},
	}
	for _, c := range equal {
		if got := Compare(c[0], c[1]); got != 0 {
			t.Errorf("Compare(%q,%q) = %d, want 0", c[0], c[1], got)
		}
	}
}

func TestParse(t *testing.T) {
	cases := []struct {
		in string
		v  Version
	}{
		{"1.2.3", Version{0, "1.2.3", ""}},
		{"1:1.2.3-4", Version{1, "1.2.3", "4"}},
		{"1.2-3-4", Version{0, "1.2-3", "4"}},
		{"2:1.0", Version{2, "1.0", ""}},
		{"x:1.0", Version{0, "x:1.0", ""}},
	}
	for _, c := range cases {
		if got := Parse(c.in); got != c.v {
			t.Errorf("Parse(%q) = %+v, want %+v", c.in, got, c.v)
		}
	}
}

func TestValid(t *testing.T) {
	good := []string{"0.2.1", "0.2.1-1", "1:2.0-3", "1.0~rc1", "0.0.0~git1a2b3c4", "1.0+dfsg-1~bpo12+1", "1.2-3-4"}
	bad := []string{"", "dev", "v0.1.0", "v0.1.0-4-gabc", "abc", ":1.0", "x:1.0", "1.0-", "1.0 beta", "1.0_1", "<html>", "1.0/2"}
	for _, v := range good {
		if !Valid(v) {
			t.Errorf("Valid(%q) = false", v)
		}
	}
	for _, v := range bad {
		if Valid(v) {
			t.Errorf("Valid(%q) = true", v)
		}
	}
}
