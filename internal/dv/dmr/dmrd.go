package dmr

import (
	"encoding/binary"
	"fmt"
)

// DMRDMagic starts every DMRD packet.
const DMRDMagic = "DMRD"

func (d *DMRD) flags() byte {
	f := byte(d.FrameType&3)<<4 | d.DTypeVSeq&0xF
	if d.Slot == 2 {
		f |= 0x80
	}
	if d.Private {
		f |= 0x40
	}
	return f
}

// Marshal returns the 55-byte packet (BER and RSSI included, zero if unset).
func (d *DMRD) Marshal() []byte { return d.AppendTo(make([]byte, 0, DMRDLen)) }

// AppendTo appends the 55-byte packet to dst.
func (d *DMRD) AppendTo(dst []byte) []byte {
	dst = append(dst, DMRDMagic...)
	dst = append(dst, d.Seq,
		byte(d.Src>>16), byte(d.Src>>8), byte(d.Src),
		byte(d.Dst>>16), byte(d.Dst>>8), byte(d.Dst))
	dst = binary.BigEndian.AppendUint32(dst, d.RepeaterID)
	dst = append(dst, d.flags())
	dst = binary.BigEndian.AppendUint32(dst, d.StreamID)
	dst = append(dst, d.Burst[:]...)
	return append(dst, d.BER, d.RSSI)
}

// ParseDMRD parses a DMRD packet of 53 or more bytes.
func ParseDMRD(p []byte) (DMRD, error) {
	var d DMRD
	if len(p) < DMRDMinLen || string(p[:4]) != DMRDMagic {
		return d, fmt.Errorf("dmr: not a DMRD packet (%d bytes)", len(p))
	}
	d.Seq = p[4]
	d.Src = uint32(p[5])<<16 | uint32(p[6])<<8 | uint32(p[7])
	d.Dst = uint32(p[8])<<16 | uint32(p[9])<<8 | uint32(p[10])
	d.RepeaterID = binary.BigEndian.Uint32(p[11:])
	f := p[15]
	d.Slot = 1
	if f&0x80 != 0 {
		d.Slot = 2
	}
	d.Private = f&0x40 != 0
	d.FrameType = FrameType(f>>4) & 3
	d.DTypeVSeq = f & 0xF
	d.StreamID = binary.BigEndian.Uint32(p[16:])
	copy(d.Burst[:], p[20:53])
	if len(p) >= DMRDLen {
		d.BER, d.RSSI, d.HasBERRSSI = p[53], p[54], true
	}
	return d, nil
}

// LC returns the LC implied by the packet header (group or private call).
func (d *DMRD) LC() LC {
	lc := LC{FLCO: FLCOGroup, Src: d.Src, Dst: d.Dst}
	if d.Private {
		lc.FLCO = FLCOPrivate
	}
	return lc
}
