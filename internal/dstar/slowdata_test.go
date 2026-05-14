package dstar

import "testing"

func TestEncodeTextMessage(t *testing.T) {
	frames := EncodeTextMessage("RefConnect by KR4GCQ")

	// Frame 0 must be the sync pattern.
	if frames[0] != SyncSlowData {
		t.Errorf("frame 0 = %v, want sync %v", frames[0], SyncSlowData)
	}

	// Descramble frames 1-8 and verify the 4 text blocks.
	want := [4]struct {
		tag  byte
		text string
	}{
		{0x40, "RefCo"},
		{0x41, "nnect"},
		{0x42, " by K"},
		{0x43, "R4GCQ"},
	}
	for block := 0; block < 4; block++ {
		f1 := uint8(1 + block*2)
		f2 := f1 + 1
		raw1 := ScrambleSlowData(frames[f1], f1) // descramble
		raw2 := ScrambleSlowData(frames[f2], f2)

		if raw1[0] != want[block].tag {
			t.Errorf("block %d type = 0x%02X, want 0x%02X", block, raw1[0], want[block].tag)
		}
		got := string([]byte{raw1[1], raw1[2], raw2[0], raw2[1], raw2[2]})
		if got != want[block].text {
			t.Errorf("block %d text = %q, want %q", block, got, want[block].text)
		}
	}

	// Frames 9-20 must be null filler (scrambled zeros).
	for i := 9; i <= MaxSeq; i++ {
		expected := NullSlowData(uint8(i))
		if frames[i] != expected {
			t.Errorf("frame %d = %v, want null %v", i, frames[i], expected)
		}
	}
}

func TestEncodeTextMessagePadsShortString(t *testing.T) {
	frames := EncodeTextMessage("Hi")

	// Block 0 should be "Hi   " (padded with spaces).
	raw1 := ScrambleSlowData(frames[1], 1)
	raw2 := ScrambleSlowData(frames[2], 2)
	got := string([]byte{raw1[1], raw1[2], raw2[0], raw2[1], raw2[2]})
	if got != "Hi   " {
		t.Errorf("block 0 text = %q, want %q", got, "Hi   ")
	}

	// Block 3 should be all spaces.
	raw1 = ScrambleSlowData(frames[7], 7)
	raw2 = ScrambleSlowData(frames[8], 8)
	got = string([]byte{raw1[1], raw1[2], raw2[0], raw2[1], raw2[2]})
	if got != "     " {
		t.Errorf("block 3 text = %q, want %q (all spaces)", got, "     ")
	}
}
