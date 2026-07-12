package dstar

import (
	"bufio"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// loadSlowDataFixture parses a "<seq>: <b0> <b1> <b2>" fixture (see
// testdata/doozy_slowdata.txt) into an array indexed by sequence number.
func loadSlowDataFixture(t *testing.T, name string) [MaxSeq + 1][3]byte {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer f.Close()

	var frames [MaxSeq + 1][3]byte
	var seen [MaxSeq + 1]bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		colon := strings.IndexByte(line, ':')
		if colon < 0 {
			t.Fatalf("malformed fixture line: %q", line)
		}
		seq, err := strconv.Atoi(strings.TrimSpace(line[:colon]))
		if err != nil || seq < 0 || seq > MaxSeq {
			t.Fatalf("bad seq in line %q: %v", line, err)
		}
		fields := strings.Fields(line[colon+1:])
		if len(fields) != 3 {
			t.Fatalf("line %q: want 3 bytes, got %d", line, len(fields))
		}
		for i, h := range fields {
			b, err := strconv.ParseUint(h, 16, 8)
			if err != nil {
				t.Fatalf("line %q: bad hex %q: %v", line, h, err)
			}
			frames[seq][i] = byte(b)
		}
		seen[seq] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	for s := 0; s <= MaxSeq; s++ {
		if !seen[s] {
			t.Fatalf("fixture missing seq %d", s)
		}
	}
	return frames
}

// TestDescrambleRealSlowData is the ground-truth acceptance oracle for the
// slow-data scrambler (GPS.md Phase 0/1). It descrambles a real IC-705
// superframe captured over the air and reassembles the 6-byte slow-data
// segments, asserting the recovered header/callsign payload matches what the
// radio actually sent (MYCALL=KR4GCQ, URCALL=CQCQCQ, RPT=DIRECT).
//
// These captures carry the D-STAR header slow-data (type 0x5n), not a D-PRS
// GPS sentence, so this validates the scrambler + segment framing directly
// against the wire. A wrong scrambler (e.g. the old per-position table) yields
// garbage here.
func TestDescrambleRealSlowData(t *testing.T) {
	frames := loadSlowDataFixture(t, "doozy_slowdata.txt")

	// Sync frame must be the literal, unscrambled sync pattern.
	if frames[0] != SyncSlowData {
		t.Fatalf("fixture seq 0 = % X, want sync % X", frames[0], SyncSlowData)
	}

	// Descramble frames 1..20 and concatenate into the 60-byte slow-data block.
	var block []byte
	for s := 1; s <= MaxSeq; s++ {
		de := ScrambleSlowData(frames[s], uint8(s))
		block = append(block, de[0], de[1], de[2])
	}

	// Reassemble 6-byte segments [header][5 data]; collect the data bytes of
	// header/callsign segments (type 0x5n).
	var payload []byte
	for i := 0; i+6 <= len(block); i += 6 {
		hdr := block[i]
		if hdr&0xF0 == 0x50 { // header/callsign segment
			payload = append(payload, block[i+1:i+6]...)
		}
	}

	got := string(payload)
	for _, want := range []string{"DIRECT", "CQCQCQ", "KR4GCQ"} {
		if !strings.Contains(got, want) {
			t.Errorf("descrambled header payload %q does not contain %q — scrambler is wrong", got, want)
		}
	}
}

// TestScramblerKeyExactBytes pins the descrambling of the first data frame to
// the exact ground-truth bytes, so an accidental revert to a per-position
// table (which matched only frame 1) still fails on frames beyond it.
func TestScramblerKeyExactBytes(t *testing.T) {
	frames := loadSlowDataFixture(t, "doozy_slowdata.txt")

	// Frame 2 on the wire is 70 0B DA; with key 70 4F 93 it descrambles to
	// 00 44 49 (the "\0DI" straddling the first header segment's data). The
	// old table used 40 64 74 here and would produce 30 6F AE instead.
	got := ScrambleSlowData(frames[2], 2)
	want := [3]byte{0x00, 0x44, 0x49}
	if got != want {
		t.Errorf("frame 2 descrambled = % X, want % X", got, want)
	}
}
