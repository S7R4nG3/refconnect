package dstar

// Slow data is carried in the 3-byte slow-data field of each DV voice frame.
// Over 20 frames, 60 bytes of slow data accumulate, forming a "slow data block".
// Each byte is first XOR'd with a scrambling sequence before transmission.
//
// D-STAR slow data types (first byte of unscrambled block):
//   0x30 - GPS/DPRS
//   0x43 - short message (text)
//   0x00 - null / filler
//
// References: D-STAR Technical Specification, Section 7.3

// scrambler is the 20-byte XOR sequence applied to each slow-data block.
// Index into it by (frame.Seq % 20).
var scrambler = [20]byte{
	0x70, 0x4F, 0x93, 0x40, 0x64, 0x74, 0x6D, 0x30, 0x2B, 0x2B,
	0xBE, 0xCC, 0x9E, 0x50, 0x00, 0x7F, 0xD5, 0x97, 0xD7, 0x22,
}

// ScrambleSlowData applies the scrambling XOR to 3 bytes of slow data
// given the frame sequence number (0–19). Scrambling is self-inverse,
// so the same function is used for both encoding and decoding.
func ScrambleSlowData(raw [3]byte, seq uint8) [3]byte {
	idx := seq % 20
	return [3]byte{
		raw[0] ^ scrambler[idx],
		raw[1] ^ scrambler[(idx+1)%20],
		raw[2] ^ scrambler[(idx+2)%20],
	}
}

// NullSlowData returns the scrambled representation of a null slow-data byte
// for the given sequence number. Use this to fill frames when you have no
// slow data to send.
func NullSlowData(seq uint8) [3]byte {
	return ScrambleSlowData([3]byte{0x00, 0x00, 0x00}, seq)
}
