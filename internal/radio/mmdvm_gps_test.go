package radio

import "testing"

// TestObserveNMEAExtractsInterleavedSentence feeds a byte stream that mixes
// MMDVM binary frame bytes with a full NMEA sentence (as the TH-D75 does when
// "PC Output (GPS)" shares the serial link) and asserts the sentence is
// recovered intact on gpsCh, with the binary bytes ignored.
func TestObserveNMEAExtractsInterleavedSentence(t *testing.T) {
	m := NewMMDVMRadio()

	var stream []byte
	stream = append(stream, 0xE0, 0x03, 0x01)                                     // MMDVM status frame (binary)
	stream = append(stream, []byte("$GPRMC,123519,A,3513.91,N,08051.27,W,0,0,120714,,*10\r\n")...) // NMEA
	stream = append(stream, 0xE0, 0x04, 0x03, 0x00)                               // more binary

	m.observeNMEA(stream)

	select {
	case got := <-m.gpsCh:
		want := "$GPRMC,123519,A,3513.91,N,08051.27,W,0,0,120714,,*10"
		if got != want {
			t.Errorf("extracted %q, want %q", got, want)
		}
	default:
		t.Fatal("no NMEA sentence extracted from interleaved stream")
	}
}

// TestObserveNMEAIgnoresBinaryDollar ensures a stray '$' inside binary data
// (immediately followed by a non-printable byte) does not emit a bogus line.
func TestObserveNMEAIgnoresBinaryDollar(t *testing.T) {
	m := NewMMDVMRadio()
	m.observeNMEA([]byte{'$', 0xE0, 0x11, 0x00, 0xAA}) // '$' then binary
	select {
	case got := <-m.gpsCh:
		t.Fatalf("emitted a sentence from binary noise: %q", got)
	default:
	}
}

// TestObserveNMEASplitAcrossReads verifies a sentence delivered across two
// separate Read calls (as happens with serial buffering) is still assembled.
func TestObserveNMEASplitAcrossReads(t *testing.T) {
	m := NewMMDVMRadio()
	m.observeNMEA([]byte("$GNGGA,123519,3513.91,N,080"))
	m.observeNMEA([]byte("51.27,W,1,08,0.9,10,M,,,,*42\r\n"))
	select {
	case got := <-m.gpsCh:
		want := "$GNGGA,123519,3513.91,N,08051.27,W,1,08,0.9,10,M,,,,*42"
		if got != want {
			t.Errorf("extracted %q, want %q", got, want)
		}
	default:
		t.Fatal("no NMEA sentence extracted across split reads")
	}
}
