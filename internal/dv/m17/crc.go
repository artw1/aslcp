// SPDX-License-Identifier: GPL-3.0-or-later

package m17

// CRC16 is the M17 CRC: polynomial 0x5935, initial value 0xFFFF, MSB first,
// no reflection, no final xor.
func CRC16(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x5935
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
