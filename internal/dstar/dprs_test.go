package dstar

import "testing"

func TestDPRSEncodeDecodeRoundTrip(t *testing.T) {
	sentence := "$$CRC29AD,KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect\r"
	frames := EncodeDPRSFrames(sentence, 0)

	var dec DPRSDecoder
	var gotSentences []string
	seq := uint8(0)
	for _, f := range frames {
		gotSentences = append(gotSentences, dec.Feed(f, seq)...)
		seq = (seq + 1) % (MaxSeq + 1)
	}

	if len(gotSentences) != 1 {
		t.Fatalf("decoder returned %d sentences, want 1: %#v", len(gotSentences), gotSentences)
	}
	if gotSentences[0] != sentence {
		t.Errorf("round-trip mismatch:\n got:  %q\n want: %q", gotSentences[0], sentence)
	}
}

func TestDPRSEncodeDecodeStartMidSuperframe(t *testing.T) {
	// Start encoding at a non-zero sequence to verify mid-superframe alignment.
	sentence := "$$CRC29AD,KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect\r"
	startSeq := uint8(5)
	frames := EncodeDPRSFrames(sentence, startSeq)

	var dec DPRSDecoder
	var gotSentences []string
	seq := startSeq
	for _, f := range frames {
		gotSentences = append(gotSentences, dec.Feed(f, seq)...)
		seq = (seq + 1) % (MaxSeq + 1)
	}

	if len(gotSentences) != 1 {
		t.Fatalf("decoder returned %d sentences, want 1: %#v", len(gotSentences), gotSentences)
	}
	if gotSentences[0] != sentence {
		t.Errorf("round-trip mismatch:\n got:  %q\n want: %q", gotSentences[0], sentence)
	}
}

func TestEncodeDPRSFramesEmitsSyncAtSeqZero(t *testing.T) {
	sentence := "$$CRC29AD,KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect\r"
	frames := EncodeDPRSFrames(sentence, 0)
	if len(frames) == 0 {
		t.Fatal("no frames emitted")
	}
	// First frame (seq=0) should be the sync pattern.
	if frames[0] != SyncSlowData {
		t.Errorf("frame 0 = %v, want sync %v", frames[0], SyncSlowData)
	}
}

func TestDPRSDecoderIgnoresNonGPSSegments(t *testing.T) {
	var dec DPRSDecoder
	// Feed two frames worth of text-type segment (0x4X header); decoder
	// should yield nothing and not crash. Use seq 1,2 to avoid sync frame.
	seg := [6]byte{0x45, 'h', 'e', 'l', 'l', 'o'}
	got := dec.Feed(ScrambleSlowData([3]byte{seg[0], seg[1], seg[2]}, 1), 1)
	got = append(got, dec.Feed(ScrambleSlowData([3]byte{seg[3], seg[4], seg[5]}, 2), 2)...)
	if len(got) != 0 {
		t.Errorf("text segment produced sentences: %#v", got)
	}
}

// TestDPRSDecoderSelfSyncsWithWrongSeq reproduces the MMDVM/TH-D75 failure
// mode: the caller's seq is unreliable (the radio package synthesizes it by
// counting frames, and it drifts). As long as the unscrambled sync triplet is
// present in the stream, the decoder must still recover the sentence by
// locking onto that pattern rather than trusting seq. The bogus seq below even
// lands on multiples of 21 for real data frames — which previously would have
// passed them through unscrambled and corrupted the segments.
func TestDPRSDecoderSelfSyncsWithWrongSeq(t *testing.T) {
	sentence := "$$CRC29AD,KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect\r"
	frames := EncodeDPRSFrames(sentence, 0)

	var dec DPRSDecoder
	var got []string
	bogus := uint8(13) // arbitrary starting offset, unrelated to real position
	for _, f := range frames {
		got = append(got, dec.Feed(f, bogus)...)
		bogus = (bogus + 1) % (MaxSeq + 1) // drifts across 0 repeatedly
	}

	if len(got) != 1 || got[0] != sentence {
		t.Fatalf("self-sync decode failed with wrong seq: got %#v, want [%q]", got, sentence)
	}
}

// TestDPRSDecoderExtractsRawNMEA covers a radio configured to embed plain
// NMEA ("$GPRMC…") in the GPS slow-data segments rather than the D-PRS
// "$$CRC…" form. Previously the decoder only searched for "$$CRC" and silently
// discarded NMEA; it must now surface any "$…\r" sentence so the router's NMEA
// fallback can parse the position.
func TestDPRSDecoderExtractsRawNMEA(t *testing.T) {
	nmea := "$GPRMC,123519,A,3513.91,N,08051.27,W,000.0,000.0,130726,,*1B\r"
	frames := EncodeDPRSFrames(nmea, 0)

	var dec DPRSDecoder
	var got []string
	seq := uint8(0)
	for _, f := range frames {
		got = append(got, dec.Feed(f, seq)...)
		seq = (seq + 1) % (MaxSeq + 1)
	}

	if len(got) != 1 || got[0] != nmea {
		t.Fatalf("raw NMEA extraction failed: got %#v, want [%q]", got, nmea)
	}
}

func TestDPRSDecoderSkipsSyncFrames(t *testing.T) {
	var dec DPRSDecoder
	// Feeding a sync frame should not produce output or disrupt state.
	got := dec.Feed(SyncSlowData, 0)
	if len(got) != 0 {
		t.Errorf("sync frame produced sentences: %#v", got)
	}
}
