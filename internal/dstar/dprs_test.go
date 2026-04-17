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

func TestDPRSDecoderSkipsSyncFrames(t *testing.T) {
	var dec DPRSDecoder
	// Feeding a sync frame should not produce output or disrupt state.
	got := dec.Feed(SyncSlowData, 0)
	if len(got) != 0 {
		t.Errorf("sync frame produced sentences: %#v", got)
	}
}
