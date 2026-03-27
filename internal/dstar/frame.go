// Package dstar implements D-STAR DV frame encoding and decoding.
// It handles the raw byte-level structures used in DSVT network packets
// and serial radio interfaces — AMBE voice frames, DV headers, and slow data.
package dstar

// DVHeader is the decoded 41-byte D-STAR DV header.
// Callsign fields are 8 characters, space-padded on the right.
type DVHeader struct {
	Flag1       byte
	Flag2       byte
	Flag3       byte
	RPT2        string // 8-char repeater/reflector callsign (outbound)
	RPT1        string // 8-char repeater callsign (local)
	YourCall    string // 8-char destination ("CQCQCQ  " for CQ)
	MyCall      string // 8-char source callsign
	MyCallSuffix string // 4-char suffix appended to MyCall
}

// DVFrame is a single 12-byte D-STAR DV voice frame.
// It carries 72 bits of AMBE+2 compressed audio and 24 bits of slow data.
type DVFrame struct {
	Seq      uint8    // 0–20 within a transmission; wraps at 21
	AMBE     [9]byte  // 72-bit AMBE+2 voice payload
	SlowData [3]byte  // 3 bytes of slow-data (see slowdata.go)
	End      bool     // true if this is the last frame of the transmission
}

// HeaderBytes is the wire size of an encoded D-STAR DV header (including CRC).
const HeaderBytes = 41

// FrameBytes is the wire size of a DV voice payload (AMBE + slow data).
const FrameBytes = 12

// MaxSeq is the maximum sequence number before it wraps back to 0.
const MaxSeq = 21

// SilenceAMBE is the standard 72-bit AMBE+2 silence pattern.
// Transmit this when audio is not available to fill a frame gracefully.
var SilenceAMBE = [9]byte{0x9E, 0x8D, 0x32, 0x88, 0x26, 0x1A, 0x3F, 0x61, 0xE8}

// CQCall is the standard D-STAR CQ destination callsign.
const CQCall = "CQCQCQ  "

// PadCallsign right-pads or truncates a callsign to exactly n characters with spaces.
func PadCallsign(s string, n int) string {
	b := make([]byte, n)
	for i := range b {
		b[i] = ' '
	}
	copy(b, []byte(s))
	return string(b)
}
