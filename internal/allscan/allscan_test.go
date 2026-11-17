package allscan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSample(t *testing.T) {
	f, err := os.Open("testdata/favorites-sample.ini")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	items := Parse(f)
	want := []Item{
		{"27339", "East Coast Reflector 27339"},
		{"43763", "N0AAT Example Hub, Metropolis, ST 43763"},
		{"3001234", "EchoLink Conference 3001234"},
		{"555491", "Single quoted 555491"},
		{"2495", "Hardcoded local node 2001"},
		{"47620", "Node specific 47620"},
	}
	if len(items) != len(want) {
		t.Fatalf("got %d: %+v", len(items), items)
	}
	for i := range want {
		if items[i] != want[i] {
			t.Errorf("item %d: got %+v want %+v", i, items[i], want[i])
		}
	}
}

func TestParseCRLFAndJunk(t *testing.T) {
	b, _ := os.ReadFile("testdata/favorites-crlf.ini")
	items := Parse(strings.NewReader(string(b)))
	if len(items) != 1 || items[0].Node != "1999" || items[0].Label != "CRLF Hub 1999" {
		t.Fatalf("%+v", items)
	}
	if got := Parse(strings.NewReader("not an ini\n\x00\x01\nlabel[ = broken\n[unterminated\n")); len(got) != 0 {
		t.Fatalf("junk produced %+v", got)
	}
	if got := Parse(strings.NewReader("")); got == nil || len(got) != 0 {
		t.Fatal("want empty non-nil slice")
	}
}

func TestExtractNode(t *testing.T) {
	cases := map[string]string{
		"rpt cmd %node% ilink 3 27339":    "27339",
		"rpt cmd 2001 ilink 13 2000":      "2000",
		"rpt cmd %node% ilink 8 3001234":  "3001234",
		"rpt fun %node% *81":              "",
		"something 123456 else":           "123456",
		"rpt cmd %node% ilink 1 99999999": "99999999",
	}
	for in, want := range cases {
		if got := ExtractNode(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	a, b := filepath.Join(root, "a"), filepath.Join(root, "b")
	os.MkdirAll(a, 0o755)
	os.MkdirAll(b, 0o755)
	sample, _ := os.ReadFile("testdata/favorites-sample.ini")
	os.WriteFile(filepath.Join(a, "favorites.ini"), []byte("[general]\n"), 0o644) // empty
	os.WriteFile(filepath.Join(b, "favorites-extra.ini"), sample, 0o644)

	res, err := Find([]string{filepath.Join(root, "missing"), a, b})
	if err != nil || !res.Found || len(res.Items) != 6 || filepath.Base(res.Path) != "favorites-extra.ini" {
		t.Fatalf("%+v %v", res, err)
	}
	res, _ = Find([]string{a})
	if !res.Found || len(res.Items) != 0 || res.Items == nil {
		t.Fatalf("empty file: %+v", res)
	}
	res, err = Find([]string{filepath.Join(root, "missing")})
	if err != nil || res.Found || res.Items == nil {
		t.Fatalf("%+v %v", res, err)
	}
	// favorites.ini sorts before favorites-extra.ini in the same dir
	os.WriteFile(filepath.Join(b, "favorites.ini"), []byte("[general]\nlabel[]=\"X\"\ncmd[]=\"rpt cmd %node% ilink 3 1111\"\n"), 0o644)
	res, _ = Find([]string{b})
	if filepath.Base(res.Path) != "favorites.ini" || len(res.Items) != 1 {
		t.Fatalf("%+v", res)
	}
}
