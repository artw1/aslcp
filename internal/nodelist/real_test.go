package nodelist

import (
	"os"
	"testing"
)

// TestParseRealExcerpt uses lines from the official ASL node list
// (allmondb), including entries with "|" inside the location and the
// generator footer comments.
func TestParseRealExcerpt(t *testing.T) {
	f, err := os.Open("testdata/astdb-excerpt.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 27 {
		t.Fatalf("got %d entries, want 27", len(got))
	}
	by := map[string]Info{}
	for _, in := range got {
		by[in.Node] = in
	}
	if in := by["2000"]; in.Callsign != "N0CALL" || in.Description != "ASL Public Hub" {
		t.Errorf("2000 = %+v", in)
	}
	if in := by["608720"]; in.Description != "** ZA-NET **" || in.Location != "Example ARC - Metropolis | ZA-NET" {
		t.Errorf("608720 = %+v", in)
	}
	if in := by["55198"]; in.Description != "cloud" || in.Location != "|Metropolis, ST" {
		t.Errorf("55198 = %+v", in)
	}
}

// TestParseFullList round-trips a complete downloaded node list when
// NODELIST_FULL points at one (not committed: ~1.4 MB).
func TestParseFullList(t *testing.T) {
	p := os.Getenv("NODELIST_FULL")
	if p == "" {
		t.Skip("set NODELIST_FULL to a full astdb.txt")
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	got, err := Parse(f)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("parsed %d nodes", len(got))
	if len(got) < 40000 {
		t.Fatalf("only %d nodes", len(got))
	}
}
