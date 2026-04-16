package dstar

import "testing"

func TestDPRSEncodeDecodeRoundTrip(t *testing.T) {
	sentence := "$$CRC29AD,KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect\r"
	frames := EncodeDPRSFrames(sentence, 0)
	// Every 5-byte chunk emits 2 frames.
	chunks := (len(sentence) + 4) / 5
	if len(frames) != chunks*2 {
		t.Fatalf("EncodeDPRSFrames emitted %d frames, want %d", len(frames), chunks*2)
	}

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

func TestDPRSDecoderIgnoresNonGPSSegments(t *testing.T) {
	var dec DPRSDecoder
	// Feed two frames worth of text-type segment (0x4X header); decoder
	// should yield nothing and not crash.
	seg := [6]byte{0x45, 'h', 'e', 'l', 'l', 'o'}
	got := dec.Feed(ScrambleSlowData([3]byte{seg[0], seg[1], seg[2]}, 0), 0)
	got = append(got, dec.Feed(ScrambleSlowData([3]byte{seg[3], seg[4], seg[5]}, 1), 1)...)
	if len(got) != 0 {
		t.Errorf("text segment produced sentences: %#v", got)
	}
}
