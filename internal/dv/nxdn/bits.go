package nxdn

// Bit helpers: bit 0 is the MSB of byte 0, as in MMDVMHost.

func getBit(p []byte, i int) bool { return p[i>>3]&(0x80>>(uint(i)&7)) != 0 }

func setBit(p []byte, i int, b bool) {
	m := byte(0x80) >> (uint(i) & 7)
	if b {
		p[i>>3] |= m
	} else {
		p[i>>3] &^= m
	}
}
