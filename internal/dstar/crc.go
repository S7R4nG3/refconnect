package dstar

// crcTable is a pre-computed lookup table for the D-STAR LSB-first (reflected)
// CRC-CCITT algorithm: poly 0x8408 (reflected 0x1021).
var crcTable [256]uint16

func init() {
	for i := range 256 {
		crc := uint16(i)
		for range 8 {
			if crc&1 != 0 {
				crc = (crc >> 1) ^ 0x8408
			} else {
				crc >>= 1
			}
		}
		crcTable[i] = crc
	}
}

// crc16CCITT computes the D-STAR header CRC using a table-driven LSB-first
// (reflected) CRC-CCITT algorithm: poly 0x8408 (reflected 0x1021), init 0xFFFF,
// final XOR 0xFFFF. Verified against USBPcap captures of Icom IC-705 DV Gateway
// Terminal frames.
func crc16CCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc = (crc >> 8) ^ crcTable[byte(crc)^b]
	}
	return ^crc
}
