package echolink

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// Control-port packets. EchoLink sends RTCP compound packets with the version
// bits set to 3: an empty receiver report followed by an SDES or a BYE, all
// with SSRC 0. The SDES items follow the convention open-source clients use:
// CNAME and EMAIL are the literal "CALLSIGN", NAME is the callsign padded to
// 15 characters followed by the operator's name, and PHONE holds a fixed
// "08:30". We also send LOC with our location, a standard SDES item that
// peers ignore if they do not use it.

const (
	rtcpRR   = 201
	rtcpSDES = 202
	rtcpBYE  = 203

	sdesEnd   = 0
	sdesCNAME = 1
	sdesNAME  = 2
	sdesEMAIL = 3
	sdesPHONE = 4
	sdesLOC   = 5
	sdesTOOL  = 6
	sdesNOTE  = 7

	rtcpVersionBits = 3 << 6
	// byeReason is the reason text EchoLink clients put in a BYE.
	byeReason = "jan2002"
)

// emptyRR is the receiver report that starts every compound packet.
var emptyRR = []byte{rtcpVersionBits, rtcpRR, 0, 1, 0, 0, 0, 0}

// pad4 appends zero bytes until len(b) is a multiple of four.
func pad4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// setLength fills in the RTCP length field of the packet starting at b.
func setLength(b []byte) {
	binary.BigEndian.PutUint16(b[2:], uint16(len(b)/4-1))
}

func addItem(b []byte, typ byte, v string) []byte {
	if len(v) > 255 {
		v = v[:255]
	}
	b = append(b, typ, byte(len(v)))
	return append(b, v...)
}

// buildSDES builds an RR+SDES compound packet identifying us.
func buildSDES(callsign, name, location string) []byte {
	out := append([]byte(nil), emptyRR...)
	sdes := []byte{rtcpVersionBits | 1, rtcpSDES, 0, 0, 0, 0, 0, 0}
	sdes = addItem(sdes, sdesCNAME, "CALLSIGN")
	sdes = addItem(sdes, sdesNAME, fmt.Sprintf("%-15s%s", callsign, name))
	sdes = addItem(sdes, sdesEMAIL, "CALLSIGN")
	sdes = addItem(sdes, sdesPHONE, "08:30")
	if location != "" {
		sdes = addItem(sdes, sdesLOC, location)
	}
	sdes = append(sdes, sdesEnd)
	sdes = pad4(sdes)
	setLength(sdes)
	return append(out, sdes...)
}

// buildBYE builds an RR+BYE compound packet.
func buildBYE() []byte {
	out := append([]byte(nil), emptyRR...)
	bye := []byte{rtcpVersionBits | 1, rtcpBYE, 0, 0, 0, 0, 0, 0}
	bye = append(bye, byte(len(byeReason)))
	bye = append(bye, byeReason...)
	bye = pad4(bye)
	setLength(bye)
	return append(out, bye...)
}

// rtcpInfo is what we read from a control packet.
type rtcpInfo struct {
	sdes  bool
	bye   bool
	items map[byte]string // SDES items of the first chunk
}

// callsign returns the station callsign from the SDES NAME item.
func (r rtcpInfo) callsign() string {
	f := strings.Fields(r.items[sdesNAME])
	if len(f) == 0 {
		return ""
	}
	return strings.ToUpper(f[0])
}

// name returns the operator name from the SDES NAME item.
func (r rtcpInfo) name() string {
	n := strings.TrimSpace(r.items[sdesNAME])
	f := strings.Fields(n)
	if len(f) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(n, f[0]))
}

// parseRTCP walks an RTCP compound packet. Unknown packet types are skipped;
// a truncated packet returns what was parsed before the damage and an error.
func parseRTCP(p []byte) (rtcpInfo, error) {
	var info rtcpInfo
	for len(p) > 0 {
		if len(p) < 4 {
			return info, fmt.Errorf("echolink: short RTCP header")
		}
		n := (int(binary.BigEndian.Uint16(p[2:])) + 1) * 4
		if n > len(p) {
			// Some peers get the length wrong; use what is there.
			n = len(p)
		}
		pkt := p[:n]
		count := int(pkt[0] & 0x1f)
		switch pkt[1] {
		case rtcpSDES:
			info.sdes = true
			if count > 0 && info.items == nil {
				info.items = parseSDESChunk(pkt[4:])
			}
		case rtcpBYE:
			info.bye = true
		}
		p = p[n:]
	}
	return info, nil
}

// parseSDESChunk reads the items of the first SDES chunk in b.
func parseSDESChunk(b []byte) map[byte]string {
	items := make(map[byte]string)
	if len(b) < 4 {
		return items
	}
	b = b[4:] // SSRC
	for len(b) >= 1 {
		typ := b[0]
		if typ == sdesEnd || len(b) < 2 {
			break
		}
		l := int(b[1])
		if 2+l > len(b) {
			break
		}
		items[typ] = string(b[2 : 2+l])
		b = b[2+l:]
	}
	return items
}
