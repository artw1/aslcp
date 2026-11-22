package dmr

import (
	"errors"
	"fmt"
)

// ErrLC is returned for a link control that fails its checks.
var ErrLC = errors.New("dmr: bad link control")

// Bytes returns the 72-bit LC: PF, R(0), FLCO(6) | FID | service options |
// dst(24) | src(24).
func (lc LC) Bytes() (b [LCLen]byte) {
	b[0] = uint8(lc.FLCO) & 0x3F
	if lc.PF {
		b[0] |= 0x80
	}
	b[1] = lc.FID
	b[2] = lc.ServiceOptions
	b[3], b[4], b[5] = byte(lc.Dst>>16), byte(lc.Dst>>8), byte(lc.Dst)
	b[6], b[7], b[8] = byte(lc.Src>>16), byte(lc.Src>>8), byte(lc.Src)
	return b
}

// ParseLC parses a 72-bit LC. Any FLCO is accepted; callers check FLCO.
func ParseLC(b []byte) (LC, error) {
	if len(b) < LCLen {
		return LC{}, fmt.Errorf("dmr: LC of %d bytes", len(b))
	}
	return LC{
		PF:             b[0]&0x80 != 0,
		FLCO:           FLCO(b[0] & 0x3F),
		FID:            b[1],
		ServiceOptions: b[2],
		Dst:            uint32(b[3])<<16 | uint32(b[4])<<8 | uint32(b[5]),
		Src:            uint32(b[6])<<16 | uint32(b[7])<<8 | uint32(b[8]),
	}, nil
}

// Private reports whether lc is a unit to unit (private) call.
func (lc LC) Private() bool { return lc.FLCO == FLCOPrivate }

// IsVoice reports whether lc is a group or private voice LC.
func (lc LC) IsVoice() bool { return lc.FLCO == FLCOGroup || lc.FLCO == FLCOPrivate }
