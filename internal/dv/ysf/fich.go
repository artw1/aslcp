// SPDX-License-Identifier: GPL-3.0-or-later

package ysf

// FICH is the frame information channel. Field layout as in MMDVMHost
// YSFFICH.cpp (and MMDVM_CM's YSFFICH for CS/CM/DT):
//
//	byte 0: FI(2) CS(2) CM(2) BN(2)
//	byte 1: BT(2) FN(3) FT(3)
//	byte 2: -(1) Dev(1) MR(3) VoIP(1) DT(2)
//	byte 3: SQL(1) DG-ID/SQ(7)
type FICH struct {
	FI   uint8 // frame information
	CS   uint8 // callsign information
	CM   uint8 // call mode
	BN   uint8 // block number
	BT   uint8 // block total
	FN   uint8 // frame number
	FT   uint8 // frame total
	Dev  bool  // low deviation
	MR   uint8 // message route
	VoIP bool
	DT   uint8 // data type
	SQL  bool
	DGID uint8 // DG-ID (squelch code)
}

// Bytes returns the four FICH information bytes.
func (f FICH) Bytes() [4]byte {
	var b [4]byte
	b[0] = (f.FI&3)<<6 | (f.CS&3)<<4 | (f.CM&3)<<2 | f.BN&3
	b[1] = (f.BT&3)<<6 | (f.FN&7)<<3 | f.FT&7
	b[2] = (f.MR&7)<<3 | f.DT&3
	if f.Dev {
		b[2] |= 0x40
	}
	if f.VoIP {
		b[2] |= 0x04
	}
	b[3] = f.DGID & 0x7F
	if f.SQL {
		b[3] |= 0x80
	}
	return b
}

// FICHFromBytes parses the four FICH information bytes.
func FICHFromBytes(b [4]byte) FICH {
	return FICH{
		FI: b[0] >> 6, CS: b[0] >> 4 & 3, CM: b[0] >> 2 & 3, BN: b[0] & 3,
		BT: b[1] >> 6, FN: b[1] >> 3 & 7, FT: b[1] & 7,
		Dev: b[2]&0x40 != 0, MR: b[2] >> 3 & 7, VoIP: b[2]&0x04 != 0, DT: b[2] & 3,
		SQL: b[3]&0x80 != 0, DGID: b[3] & 0x7F,
	}
}

// Encode writes the coded FICH into frame[5:30] (frame is a 120-byte YSF
// frame; the sync is not touched).
func (f FICH) Encode(frame []byte) {
	var m [6]byte
	b := f.Bytes()
	copy(m[:], b[:])
	addCRC(m[:])
	w := [4]uint32{
		uint32(m[0])<<4 | uint32(m[1])>>4,
		uint32(m[1]&0x0F)<<8 | uint32(m[2]),
		uint32(m[3])<<4 | uint32(m[4])>>4,
		uint32(m[4]&0x0F)<<8 | uint32(m[5]),
	}
	var in [13]byte
	for i, d := range w {
		c := Golay24Encode(d)
		in[3*i], in[3*i+1], in[3*i+2] = byte(c>>16), byte(c>>8), byte(c)
	}
	codeBlock(in[:], 100, table520, frame[SyncLen:SyncLen+FICHLen])
}

// DecodeFICH decodes the FICH of a 120-byte YSF frame. ok is false when a
// Golay word is uncorrectable or the CRC fails.
func DecodeFICH(frame []byte) (f FICH, ok bool) {
	out := decodeBlock(frame[SyncLen:SyncLen+FICHLen], 100, table520, 96)
	var m [6]byte
	for i := 0; i < 4; i++ {
		d, good := Golay24Decode(uint32(out[3*i])<<16 | uint32(out[3*i+1])<<8 | uint32(out[3*i+2]))
		if !good {
			return FICH{}, false
		}
		switch i {
		case 0:
			m[0], m[1] = byte(d>>4), byte(d<<4)
		case 1:
			m[1] |= byte(d >> 8 & 0x0F)
			m[2] = byte(d)
		case 2:
			m[3], m[4] = byte(d>>4), byte(d<<4)
		case 3:
			m[4] |= byte(d >> 8 & 0x0F)
			m[5] = byte(d)
		}
	}
	if !checkCRC(m[:]) {
		return FICH{}, false
	}
	return FICHFromBytes([4]byte{m[0], m[1], m[2], m[3]}), true
}
