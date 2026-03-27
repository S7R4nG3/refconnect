package dstar

// crc16CCITT computes the CRC-CCITT (poly 0x1021, init 0xFFFF) checksum
// used in D-STAR DV headers.  The result is bit-inverted before storage,
// matching the convention used by most D-STAR implementations.
func crc16CCITT(data []byte) uint16 {
	crc := uint16(0xFFFF)
	for _, b := range data {
		crc ^= uint16(b) << 8
		for i := 0; i < 8; i++ {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return ^crc
}
