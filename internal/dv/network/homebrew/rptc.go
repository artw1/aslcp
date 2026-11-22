package homebrew

import (
	"fmt"
	"strings"
)

// field returns s left-justified, space padded / truncated to n bytes.
func field(s string, n int) string {
	if len(s) > n {
		s = s[:n]
	}
	return s + strings.Repeat(" ", n-len(s))
}

type rptcInfo struct {
	ID          uint32
	Callsign    string
	RxFreq      uint32
	TxFreq      uint32
	Power       int
	ColourCode  int
	Lat, Lon    float64
	Height      int
	Location    string
	Description string
	Slots       byte
	URL         string
	Software    string
	Package     string
}

// marshalRPTC builds the 302-byte config packet (layout from MMDVMHost
// DMRNetwork::writeConfig): "RPTC", id(4), callsign 8, rx 9, tx 9, power 2,
// colour code 2, lat 8, lon 9, height 3, location 20, description 19,
// slots 1, URL 124, software 40, package 40.
//
// Verified against DMRGateway DMRNetwork.cpp/DMRGateway.cpp (getConfig,
// "%-8.8s%09u%09u%02u%02u%8.8s%9.9s%03d%-20.20s%-19.19s%c%-124.124s%-40.40s%-40.40s")
// and hblink3 (fixed offsets, 302 bytes). Latitude and longitude are "%08f" and
// "%09f" cut to 8 and 9 bytes, so an unset position is "0.000000" and
// "00.000000" as MMDVMHost/DMRGateway send it. 0 Hz frequencies are sent as
// "000000000"; hblink3 and FreeDMR only store them, masters that validate are
// expected to be given rx_freq/tx_freq options.
func marshalRPTC(c rptcInfo) []byte {
	pw := c.Power
	if pw < 0 || pw > 99 {
		pw = 1
	}
	s := fmt.Sprintf("RPTC%s%s%09d%09d%02d%02d%s%s%03d%s%s%c%s%s%s",
		string(be32(c.ID)),
		field(c.Callsign, 8), c.RxFreq%1000000000, c.TxFreq%1000000000, pw, c.ColourCode%100,
		fixed(fmt.Sprintf("%08f", clamp(c.Lat, -90, 90)), 8), fixed(fmt.Sprintf("%09f", clamp(c.Lon, -180, 180)), 9),
		c.Height%1000, field(c.Location, 20), field(c.Description, 19), c.Slots,
		field(c.URL, 124), field(c.Software, 40), field(c.Package, 40))
	return []byte(s)
}

func be32(v uint32) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// fixed cuts s to n bytes (C's %8.8s).
func fixed(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
