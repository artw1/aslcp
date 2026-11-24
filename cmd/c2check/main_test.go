// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"math"
	"testing"
)

func TestSNR(t *testing.T) {
	a := []int16{1000, -1000, 500}
	if !math.IsInf(snr(a, a), 1) {
		t.Fatal("identical signals must give +Inf")
	}
	b := []int16{1010, -1010, 510}
	if got := snr(a, b); got < 35 || got > 45 {
		t.Fatalf("snr = %v", got)
	}
}

func TestBitRange(t *testing.T) {
	f := []byte{0xC0, 0x80, 0, 0, 0, 0, 0, 0x01}
	if bitRange(f, 0, 2) != 3 || bitRange(f, 2, 9) != 1 || bitRange(f, 14, 64) != 1 {
		t.Fatal("bitRange")
	}
}
