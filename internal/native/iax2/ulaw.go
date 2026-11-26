package iax2

// G.711 mu-law (ulaw) codec, written from the ITU-T G.711 segment rules:
// bias 132, clip at 32635, eight segments of sixteen steps, bits inverted
// on the wire.

const (
	ulawBias = 0x84
	ulawClip = 32635
)

var (
	ulawDecodeTable [256]int16
	ulawEncodeTable [65536]uint8 // indexed by uint16(sample)
)

func init() {
	for i := range ulawDecodeTable {
		ulawDecodeTable[i] = ulawDecodeOne(uint8(i))
	}
	for i := range ulawEncodeTable {
		ulawEncodeTable[i] = ulawEncodeOne(int16(uint16(i)))
	}
}

func ulawDecodeOne(u uint8) int16 {
	u = ^u
	exp := (u >> 4) & 7
	mant := int32(u & 0x0f)
	t := ((mant << 3) + ulawBias) << exp
	if u&0x80 != 0 {
		return int16(ulawBias - t)
	}
	return int16(t - ulawBias)
}

func ulawEncodeOne(s int16) uint8 {
	v := int32(s)
	sign := uint8(0)
	if v < 0 {
		v = -v
		sign = 0x80
	}
	if v > ulawClip {
		v = ulawClip
	}
	v += ulawBias
	exp := uint8(7)
	for mask := int32(0x4000); v&mask == 0 && exp > 0; mask >>= 1 {
		exp--
	}
	mant := uint8(v>>(exp+3)) & 0x0f
	return ^(sign | exp<<4 | mant)
}

// ULawDecode returns the linear value of one ulaw byte.
func ULawDecode(u uint8) int16 { return ulawDecodeTable[u] }

// ULawEncode returns the ulaw byte for one linear sample.
func ULawEncode(s int16) uint8 { return ulawEncodeTable[uint16(s)] }

// EncodeFrame appends the ulaw encoding of f to dst.
func EncodeFrame(dst []byte, f *Frame) []byte {
	for _, s := range f {
		dst = append(dst, ulawEncodeTable[uint16(s)])
	}
	return dst
}

// DecodeULaw splits a ulaw payload into 20 ms frames. A short final frame is
// padded with silence.
func DecodeULaw(b []byte) []Frame {
	n := (len(b) + FrameSamples - 1) / FrameSamples
	out := make([]Frame, n)
	for i, u := range b {
		out[i/FrameSamples][i%FrameSamples] = ulawDecodeTable[u]
	}
	return out
}
