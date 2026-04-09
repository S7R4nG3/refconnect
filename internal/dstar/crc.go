package dstar

// crc16CCITT computes the D-STAR header CRC using the LSB-first (reflected)
// CRC-CCITT algorithm: poly 0x8408 (reflected 0x1021), init 0xFFFF, final XOR 0xFFFF.
// Verified against USBPcap captures of Icom IC-705 DV Gateway Terminal frames.
func crc16CCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b)
		for i := 0; i < 8; i++ {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
	}
	return ^crc
}
