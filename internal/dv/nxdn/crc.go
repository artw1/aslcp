package nxdn

// CRCs over a bit string (MSB first, as MMDVMHost NXDNCRC.cpp).

// CRC6 is the SACCH CRC: poly 0x27, init 0x3F.
func CRC6(in []byte, nbits int) uint8 {
	crc := uint8(0x3F)
	for i := 0; i < nbits; i++ {
		b1 := getBit(in, i)
		b2 := crc&0x20 != 0
		crc <<= 1
		if b1 != b2 {
			crc ^= 0x27
		}
	}
	return crc & 0x3F
}

// CRC12 is the FACCH1 CRC: poly 0x80F, init 0xFFF.
func CRC12(in []byte, nbits int) uint16 {
	crc := uint16(0x0FFF)
	for i := 0; i < nbits; i++ {
		b1 := getBit(in, i)
		b2 := crc&0x0800 != 0
		crc <<= 1
		if b1 != b2 {
			crc ^= 0x080F
		}
	}
	return crc & 0x0FFF
}

// CRC15 is the UDCH CRC: poly 0x4CC5, init 0x7FFF.
func CRC15(in []byte, nbits int) uint16 {
	crc := uint16(0x7FFF)
	for i := 0; i < nbits; i++ {
		b1 := getBit(in, i)
		b2 := crc&0x4000 != 0
		crc <<= 1
		if b1 != b2 {
			crc ^= 0x4CC5
		}
	}
	return crc & 0x7FFF
}

func putBits(p []byte, at, n int, v uint32) {
	for i := 0; i < n; i++ {
		setBit(p, at+i, v>>(uint(n-1-i))&1 != 0)
	}
}

func getBits(p []byte, at, n int) uint32 {
	var v uint32
	for i := 0; i < n; i++ {
		v <<= 1
		if getBit(p, at+i) {
			v |= 1
		}
	}
	return v
}

// EncodeCRC6 writes the CRC6 of the first nbits at bits nbits..nbits+5 of in.
func EncodeCRC6(in []byte, nbits int) { putBits(in, nbits, 6, uint32(CRC6(in, nbits))) }

// CheckCRC6 verifies the CRC6 stored after nbits bits.
func CheckCRC6(in []byte, nbits int) bool {
	return getBits(in, nbits, 6) == uint32(CRC6(in, nbits))
}

// EncodeCRC12 writes the CRC12 after nbits bits.
func EncodeCRC12(in []byte, nbits int) { putBits(in, nbits, 12, uint32(CRC12(in, nbits))) }

// CheckCRC12 verifies the CRC12 stored after nbits bits.
func CheckCRC12(in []byte, nbits int) bool {
	return getBits(in, nbits, 12) == uint32(CRC12(in, nbits))
}

// EncodeCRC15 writes the CRC15 after nbits bits.
func EncodeCRC15(in []byte, nbits int) { putBits(in, nbits, 15, uint32(CRC15(in, nbits))) }

// CheckCRC15 verifies the CRC15 stored after nbits bits.
func CheckCRC15(in []byte, nbits int) bool {
	return getBits(in, nbits, 15) == uint32(CRC15(in, nbits))
}
