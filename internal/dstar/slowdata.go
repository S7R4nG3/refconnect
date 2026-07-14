package dstar

// Slow data is carried in the 3-byte slow-data field of each DV voice frame.
// Over 21 frames (one superframe), 63 bytes of slow data accumulate:
//   - Frame 0: sync pattern {0x55, 0x2D, 0x16} (NOT scrambled)
//   - Frames 1–20: scrambled payload (60 bytes)
//
// Each payload byte is XOR'd with a fixed 3-byte scrambling key before
// transmission. The key repeats on every non-sync frame (it is NOT a
// per-position table); the sync frame is never scrambled.
//
// D-STAR slow data types (high nibble of the first byte of an unscrambled
// 6-byte segment; low nibble is the segment data length):
//   0x3n - GPS / D-PRS
//   0x4n - short message (text)
//   0x5n - header / callsign
//   0x0n / 0x6n - null / filler
//
// References: D-STAR Technical Specification, Section 7.3

// SyncSlowData is the 3-byte sync pattern transmitted (unscrambled) in
// frame 0 of every D-STAR superframe. Receivers use this to locate the
// start of a slow-data block.
var SyncSlowData = [3]byte{0x55, 0x2D, 0x16}

// scramblerKey is the fixed 3-byte XOR key applied to every non-sync
// slow-data frame (seq 1–20). D-STAR repeats this same key on each frame's
// 3 bytes — it is not a per-position table.
//
// Verified against ground-truth IC-705 captures (pcaps/doozy-cap.pcapng and
// pcaps/radio-comm.pcapng): descrambling a full slow-data superframe with
// this key yields the readable D-STAR header/callsign segments
// (RPT2/RPT1="DIRECT", URCALL="CQCQCQ", MYCALL="KR4GCQ"). The previous
// 60-byte table matched only the first frame and produced garbage after —
// which also made outbound beacon/text slow data unreadable. See GPS.md.
var scramblerKey = [3]byte{0x70, 0x4F, 0x93}

// ScrambleSlowData applies the scrambling XOR to 3 bytes of slow data
// given the frame sequence number (0–20). Scrambling is self-inverse,
// so the same function is used for both encoding and decoding.
//
// Frame 0 is the sync frame and is returned unchanged (no scrambling).
func ScrambleSlowData(raw [3]byte, seq uint8) [3]byte {
	if seq%21 == 0 {
		return raw // sync frame — not scrambled
	}
	return [3]byte{
		raw[0] ^ scramblerKey[0],
		raw[1] ^ scramblerKey[1],
		raw[2] ^ scramblerKey[2],
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

// EncodeTextMessage encodes a D-STAR slow data text message (up to 20
// characters) into a full superframe of pre-scrambled slow data triplets.
// The returned array is indexed by frame sequence number (0–20):
//
//	Frame 0:   sync pattern
//	Frames 1–8: 4 text blocks (type 0x40–0x43), 2 frames per block
//	Frames 9–20: null filler
//
// Each block carries 5 characters preceded by a type byte (0x40 | blockNum),
// matching the D-STAR "short message" encoding used by BlueDV and other
// dongle clients. Messages shorter than 20 characters are right-padded
// with spaces.
func EncodeTextMessage(msg string) [MaxSeq + 1][3]byte {
	var padded [20]byte
	for i := range padded {
		padded[i] = ' '
	}
	copy(padded[:], msg)

	var frames [MaxSeq + 1][3]byte
	frames[0] = SyncSlowData

	for block := 0; block < 4; block++ {
		off := block * 5
		tag := byte(0x40) | byte(block)
		f1 := uint8(1 + block*2)
		f2 := f1 + 1
		frames[f1] = ScrambleSlowData([3]byte{tag, padded[off], padded[off+1]}, f1)
		frames[f2] = ScrambleSlowData([3]byte{padded[off+2], padded[off+3], padded[off+4]}, f2)
	}

	for i := 9; i <= MaxSeq; i++ {
		frames[i] = NullSlowData(uint8(i))
	}
	return frames
}
