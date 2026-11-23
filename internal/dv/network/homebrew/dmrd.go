package homebrew

import "encoding/binary"

const dmrdLen = 55

// Frame types (flags bits 5-4).
const (
	ftVoice     = 0
	ftVoiceSync = 1
	ftDataSync  = 2
)

// Data types for ftDataSync packets (flags bits 3-0).
const (
	dtVoiceHeader = 1
	dtTerminator  = 2
)

// dmrd is one decoded DMRD packet.
type dmrd struct {
	Seq      byte
	Src, Dst uint32
	Repeater uint32
	Slot     int // 1 or 2
	Private  bool
	FrameTyp int
	DType    int // data type, or voice sequence 0..5
	Stream   uint32
	Payload  [33]byte
	BER      byte
	RSSI     byte
}

func (d *dmrd) marshal() []byte {
	b := make([]byte, dmrdLen)
	copy(b, "DMRD")
	b[4] = d.Seq
	put24(b[5:], d.Src)
	put24(b[8:], d.Dst)
	binary.BigEndian.PutUint32(b[11:], d.Repeater)
	var f byte
	if d.Slot == 2 {
		f |= 0x80
	}
	if d.Private {
		f |= 0x40
	}
	f |= byte(d.FrameTyp&3) << 4
	f |= byte(d.DType & 0x0f)
	b[15] = f
	binary.BigEndian.PutUint32(b[16:], d.Stream)
	copy(b[20:53], d.Payload[:])
	b[53], b[54] = d.BER, d.RSSI
	return b
}

func parseDMRD(b []byte) (dmrd, bool) {
	var d dmrd
	if len(b) < dmrdLen || string(b[:4]) != "DMRD" {
		return d, false
	}
	d.Seq = b[4]
	d.Src = get24(b[5:])
	d.Dst = get24(b[8:])
	d.Repeater = binary.BigEndian.Uint32(b[11:])
	f := b[15]
	d.Slot = 1
	if f&0x80 != 0 {
		d.Slot = 2
	}
	d.Private = f&0x40 != 0
	d.FrameTyp = int(f>>4) & 3
	d.DType = int(f & 0x0f)
	d.Stream = binary.BigEndian.Uint32(b[16:])
	copy(d.Payload[:], b[20:53])
	d.BER, d.RSSI = b[53], b[54]
	return d, true
}

func put24(b []byte, v uint32) { b[0], b[1], b[2] = byte(v>>16), byte(v>>8), byte(v) }
func get24(b []byte) uint32    { return uint32(b[0])<<16 | uint32(b[1])<<8 | uint32(b[2]) }
