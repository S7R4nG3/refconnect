package dstar

// Slow data is carried in the 3-byte slow-data field of each DV voice frame.
// Over 21 frames (one superframe), 63 bytes of slow data accumulate:
//   - Frame 0: sync pattern {0x55, 0x2D, 0x16} (NOT scrambled)
//   - Frames 1–20: scrambled payload (60 bytes)
//
// Each payload byte is XOR'd with a scrambling sequence before transmission.
// The scrambler is the standard D-STAR PRBS from the G4KLX reference
// implementation (OpenDV / ircDDBGateway).
//
// D-STAR slow data types (first byte of unscrambled block):
//   0x30 - GPS/DPRS
//   0x43 - short message (text)
//   0x00 - null / filler
//
// References: D-STAR Technical Specification, Section 7.3

// SyncSlowData is the 3-byte sync pattern transmitted (unscrambled) in
// frame 0 of every D-STAR superframe. Receivers use this to locate the
// start of a slow-data block.
var SyncSlowData = [3]byte{0x55, 0x2D, 0x16}

// scrambler is the 60-byte XOR sequence applied to frames 1–20 of each
// superframe. Frame n (1 ≤ n ≤ 20) uses bytes [(n-1)*3 .. (n-1)*3+2].
// Source: G4KLX OpenDV DStarDefines / ircDDBGateway PRBS table.
var scrambler = [60]byte{
	0x70, 0x4F, 0x93, // frame 1
	0x40, 0x64, 0x74, // frame 2
	0x6D, 0x30, 0x2B, // frame 3
	0x6B, 0x6E, 0x38, // frame 4
	0x68, 0xBA, 0xCC, // frame 5
	0x9E, 0x50, 0x00, // frame 6
	0x7F, 0xD5, 0x97, // frame 7
	0xD7, 0x22, 0x5F, // frame 8
	0x06, 0x92, 0x7A, // frame 9
	0x87, 0x5B, 0x19, // frame 10
	0x83, 0x07, 0xE3, // frame 11
	0xDD, 0xCA, 0xB6, // frame 12
	0xCA, 0xD0, 0x0C, // frame 13
	0x54, 0x91, 0xE7, // frame 14
	0x35, 0x4D, 0x2C, // frame 15
	0x29, 0xE8, 0x22, // frame 16
	0x49, 0xF3, 0xC8, // frame 17
	0xDB, 0x4E, 0x62, // frame 18
	0xE6, 0xF3, 0xFB, // frame 19
	0x85, 0x08, 0xD3, // frame 20
}

// ScrambleSlowData applies the scrambling XOR to 3 bytes of slow data
// given the frame sequence number (0–20). Scrambling is self-inverse,
// so the same function is used for both encoding and decoding.
//
// Frame 0 is the sync frame and is returned unchanged (no scrambling).
func ScrambleSlowData(raw [3]byte, seq uint8) [3]byte {
	s := seq % 21
	if s == 0 {
		return raw // sync frame — not scrambled
	}
	idx := int(s-1) * 3
	return [3]byte{
		raw[0] ^ scrambler[idx],
		raw[1] ^ scrambler[idx+1],
		raw[2] ^ scrambler[idx+2],
	}
}

// NullSlowData returns the scrambled representation of a null slow-data
// triplet for the given sequence number. For frame 0 this returns the
// sync pattern; for frames 1–20 it returns scrambled zeros.
func NullSlowData(seq uint8) [3]byte {
	s := seq % 21
	if s == 0 {
		return SyncSlowData
	}
	return ScrambleSlowData([3]byte{0x00, 0x00, 0x00}, s)
}
