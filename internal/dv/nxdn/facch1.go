package nxdn

// FACCH1 carries one 80-bit Layer 3 message with a CRC-12 (92 bits), coded
// to 144 channel bits. Raw is the 12-byte form (10 data bytes, CRC-12 in
// bits 80..91, last 4 bits zero) that network payloads carry.

// FACCH1Raw returns the 12 raw bytes for msg.
func FACCH1Raw(msg [10]byte) (r [12]byte) {
	copy(r[:], msg[:])
	EncodeCRC12(r[:], 80)
	return r
}

// FACCH1CRCOK reports whether raw (at least 12 bytes) has a valid CRC-12.
func FACCH1CRCOK(raw []byte) bool { return CheckCRC12(raw, 80) }

var (
	facchInterleave [FACCHBits]int
	facchPuncture   [48]int
)

func init() {
	for i := range facchInterleave {
		facchInterleave[i] = (i%16)*9 + i/16
	}
	for k := range facchPuncture {
		facchPuncture[k] = 1 + 4*k
	}
}

// EncodeFACCH1 writes msg into an RF frame at bit offset (FACCH1Offset1 or
// FACCH1Offset2).
func EncodeFACCH1(frame []byte, offset int, msg [10]byte) {
	t1 := FACCH1Raw(msg)
	t2 := ConvEncode(t1[:], 96) // 192 bits
	var t3 [18]byte
	n, idx := 0, 0
	for i := 0; i < 192; i++ {
		if idx < len(facchPuncture) && i == facchPuncture[idx] {
			idx++
			continue
		}
		setBit(t3[:], n, getBit(t2, i))
		n++
	}
	for i := 0; i < FACCHBits; i++ {
		setBit(frame, facchInterleave[i]+offset, getBit(t3[:], i))
	}
}

// DecodeFACCH1 reads the FACCH1 at bit offset; ok reports the CRC-12.
func DecodeFACCH1(frame []byte, offset int) (msg [10]byte, ok bool) {
	var t1 [18]byte
	for i := 0; i < FACCHBits; i++ {
		setBit(t1[:], i, getBit(frame, facchInterleave[i]+offset))
	}
	var t2 [210]uint8
	n, idx := 0, 0
	for i := 0; i < FACCHBits; i++ {
		if idx < len(facchPuncture) && n == facchPuncture[idx] {
			t2[n] = 1
			n++
			idx++
		}
		if getBit(t1[:], i) {
			t2[n] = 2
		}
		n++
	}
	var v Viterbi
	v.Start()
	n = 0
	for i := 0; i < 100; i++ {
		v.Decode(t2[n], t2[n+1])
		n += 2
	}
	var out [12]byte
	v.Chainback(out[:], 96)
	copy(msg[:], out[:10])
	return msg, CheckCRC12(out[:], 80)
}
