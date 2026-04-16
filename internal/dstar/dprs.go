package dstar

// DPRS encoding and decoding over D-STAR slow data.
//
// A DPRS sentence (e.g. "$$CRC29AD,KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/...\r")
// is chunked into 5-byte payload pieces. Each piece is preceded by a 1-byte
// "GPS-data" segment header (0x30 | length, where length is the number of
// payload bytes in this segment, 1–5). The resulting 6-byte segment spans
// two consecutive voice frames of slow data (3 bytes each) and is scrambled
// with the standard D-STAR slow-data scrambler before transmission.
//
// Decoding is the inverse: accumulate unscrambled slow-data bytes, peel off
// segment headers, and concatenate payload bytes until a full $$CRC...\r
// sentence emerges.

// gpsSegHeaderMask is the high nibble that marks a GPS-data slow-data segment.
const gpsSegHeaderMask = 0x30

// EncodeDPRSFrames chunks a DPRS sentence into scrambled slow-data triplets,
// one per outgoing D-STAR voice frame. startSeq is the sequence number of
// the first emitted frame; each subsequent frame increments seq (mod 21),
// matching how the voice TX path numbers frames.
//
// Returns a slice of [3]byte triplets (already scrambled) ready to drop
// into consecutive DVFrame.SlowData fields. The number of triplets is
// always even (segments are 2 frames long); pad with NullSlowData if you
// need to align the beacon transmission to a specific frame count.
func EncodeDPRSFrames(sentence string, startSeq uint8) [][3]byte {
	data := []byte(sentence)
	var out [][3]byte
	seq := startSeq
	for i := 0; i < len(data); i += 5 {
		end := i + 5
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]
		// Build the 6-byte unscrambled segment: header + up-to-5 payload bytes,
		// right-padded with 0x00 if the chunk is shorter than 5.
		var seg [6]byte
		seg[0] = gpsSegHeaderMask | byte(len(chunk))
		copy(seg[1:], chunk)
		// Emit across two consecutive voice frames.
		out = append(out, ScrambleSlowData([3]byte{seg[0], seg[1], seg[2]}, seq))
		seq = (seq + 1) % (MaxSeq + 1)
		out = append(out, ScrambleSlowData([3]byte{seg[3], seg[4], seg[5]}, seq))
		seq = (seq + 1) % (MaxSeq + 1)
	}
	return out
}

// DPRSDecoder accumulates slow-data bytes across voice frames and yields
// complete DPRS sentences (including the "$$CRC" prefix and trailing "\r")
// as they are assembled. It is not safe for concurrent use.
type DPRSDecoder struct {
	// halfSeg holds the first 3 unscrambled bytes of a 6-byte segment while
	// we wait for the second frame.
	halfSeg    [3]byte
	haveHalf   bool
	// buf is the running payload accumulated from segment bodies. A sentence
	// is considered complete once it contains a "$$CRC" prefix and a "\r".
	buf []byte
}

// Feed processes one frame's worth of scrambled slow data. It returns any
// complete DPRS sentences that finished as a result of this frame; zero,
// one, or occasionally more sentences may be returned per call.
func (d *DPRSDecoder) Feed(scrambled [3]byte, seq uint8) []string {
	unscrambled := ScrambleSlowData(scrambled, seq)
	if !d.haveHalf {
		d.halfSeg = unscrambled
		d.haveHalf = true
		return nil
	}
	// Combine with previous half to form a 6-byte segment.
	seg := [6]byte{
		d.halfSeg[0], d.halfSeg[1], d.halfSeg[2],
		unscrambled[0], unscrambled[1], unscrambled[2],
	}
	d.haveHalf = false

	header := seg[0]
	if header&0xF0 != gpsSegHeaderMask {
		// Not a GPS-data segment; ignore but don't reset partial sentences —
		// real radios interleave GPS segments with other slow-data types.
		return nil
	}
	n := int(header & 0x0F)
	if n < 1 || n > 5 {
		return nil
	}
	d.buf = append(d.buf, seg[1:1+n]...)
	return d.extractSentences()
}

// extractSentences pulls any complete DPRS sentences from the buffer and
// returns them, leaving unterminated trailing data in place.
func (d *DPRSDecoder) extractSentences() []string {
	var out []string
	for {
		start := indexOf(d.buf, []byte("$$CRC"))
		if start < 0 {
			// No start marker — trim the buffer so it doesn't grow unbounded.
			if len(d.buf) > 256 {
				d.buf = d.buf[len(d.buf)-16:]
			}
			return out
		}
		end := indexByte(d.buf, '\r', start)
		if end < 0 {
			// Sentence still in progress; keep waiting.
			// Drop anything before the start marker to bound memory.
			if start > 0 {
				d.buf = d.buf[start:]
			}
			return out
		}
		out = append(out, string(d.buf[start:end+1]))
		d.buf = d.buf[end+1:]
	}
}

// Reset discards any accumulated state. Call this at the start of a new
// transmission (after a header) to avoid stitching fragments from different
// streams.
func (d *DPRSDecoder) Reset() {
	d.halfSeg = [3]byte{}
	d.haveHalf = false
	d.buf = d.buf[:0]
}

// indexOf is a simple byte-slice search; avoids pulling in bytes.Index to
// keep the dstar package dependency-free.
func indexOf(haystack, needle []byte) int {
	if len(needle) == 0 {
		return 0
	}
outer:
	for i := 0; i+len(needle) <= len(haystack); i++ {
		for j := 0; j < len(needle); j++ {
			if haystack[i+j] != needle[j] {
				continue outer
			}
		}
		return i
	}
	return -1
}

// indexByte finds b in haystack starting at from; -1 if not found.
func indexByte(haystack []byte, b byte, from int) int {
	for i := from; i < len(haystack); i++ {
		if haystack[i] == b {
			return i
		}
	}
	return -1
}
