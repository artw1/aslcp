package alsa

import (
	"slices"
	"testing"
)

func TestDuplicate(t *testing.T) {
	got := duplicate(nil, []int16{1, -2, 3}, 2)
	if want := []int16{1, 1, -2, -2, 3, 3}; !slices.Equal(got, want) {
		t.Fatalf("stereo: got %v want %v", got, want)
	}
	got = duplicate(got[:0], []int16{5, 6}, 1)
	if want := []int16{5, 6}; !slices.Equal(got, want) {
		t.Fatalf("mono: got %v want %v", got, want)
	}
}

func TestLeftOf(t *testing.T) {
	got := leftOf(nil, []int16{1, 100, 2, 200, 3, 300}, 2)
	if want := []int16{1, 2, 3}; !slices.Equal(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	rt := leftOf(nil, duplicate(nil, []int16{7, 8, 9}, 2), 2)
	if want := []int16{7, 8, 9}; !slices.Equal(rt, want) {
		t.Fatalf("round trip: got %v", rt)
	}
}

func TestPrefillSamples(t *testing.T) {
	m, s := prefillSamples(1), prefillSamples(2)
	if m != FrameSamples*resFactor*prefillFrames || s != 2*m {
		t.Fatalf("mono %d stereo %d", m, s)
	}
}
