package dmr

import "fmt"

// CRC masks applied to the RS(12,9) parity of the full LC (ETSI TS 102 361-1
// B.3.12, table B.21).
const (
	MaskVoiceLCHeader    = 0x96
	MaskTerminatorWithLC = 0x99
)

func lcMask(dt DataType) (byte, bool) {
	switch dt {
	case DataVoiceLCHeader:
		return MaskVoiceLCHeader, true
	case DataTerminatorWithLC:
		return MaskTerminatorWithLC, true
	}
	return 0, false
}

// LCBurst builds a voice LC header or terminator with LC data burst: BPTC of
// the LC with its masked RS(12,9) parity, the slot type and BS data sync.
func LCBurst(lc LC, dt DataType, cc uint8) Burst {
	return lcBurst(lc, dt, cc, SyncBSData)
}

func lcBurst(lc LC, dt DataType, cc uint8, sync Sync) Burst {
	var b Burst
	mask, _ := lcMask(dt)
	raw := lc.Bytes()
	p := RS129(raw)
	var info [12]byte
	copy(info[:], raw[:])
	info[9], info[10], info[11] = p[0]^mask, p[1]^mask, p[2]^mask
	b.PutBPTC196(info)
	b.PutSlotType(SlotType{CC: cc & 0xF, DataType: dt})
	b.PutSync(sync)
	return b
}

// ParseLCBurst decodes a voice LC header or terminator using the burst's own
// slot type.
func ParseLCBurst(b *Burst) (LC, SlotType, error) {
	st, ok := b.SlotType()
	if !ok {
		return LC{}, st, fmt.Errorf("dmr: bad slot type")
	}
	lc, err := DecodeLCBurst(b, st.DataType)
	return lc, st, err
}

// DecodeLCBurst decodes the full LC of a voice LC header or terminator whose
// data type is known (from the network framing), ignoring the slot type.
func DecodeLCBurst(b *Burst, dt DataType) (LC, error) {
	mask, ok := lcMask(dt)
	if !ok {
		return LC{}, fmt.Errorf("dmr: data type %d carries no full LC", dt)
	}
	info, _, _ := b.BPTC196()
	var cw [12]byte
	copy(cw[:], info[:])
	cw[9] ^= mask
	cw[10] ^= mask
	cw[11] ^= mask
	if !RS129Correct(&cw) {
		return LC{}, ErrLC
	}
	return ParseLC(cw[:9])
}
