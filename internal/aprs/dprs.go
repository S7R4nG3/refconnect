package aprs

import (
	"fmt"
	"strings"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// dprsCRC computes the checksum for the DPRS "$$CRC" sentence. D-PRS uses the
// reflected CRC-CCITT (the same algorithm as the D-STAR header/slow-data CRC),
// NOT a byte-sum — reflectors and ircDDBGateway that validate the checksum
// drop sentences whose CRC doesn't match, which is why byte-sum beacons never
// reached the reflector even though the raw TNC2 packet reached APRS-IS.
//
// The CRC covers the TNC2 payload PLUS the terminating "\r". This was confirmed
// on-air against an IC-705 D-PRS sentence
// ("$$CRCD247,KR4GCQ>API705,DSTAR*:!3513.91N/08051.27W[/\r"): CRC16CCITT over
// the body alone gave 0x251E, but over body+"\r" it gave 0xD247, matching the
// radio. Callers pass the payload without the trailing CR; this function adds
// it for the checksum.
func dprsCRC(payload string) uint16 {
	return dstar.CRC16CCITT([]byte(payload + "\r"))
}

// WrapDPRS produces a full DPRS sentence suitable for injection into the
// D-STAR slow-data stream:
//
//	$$CRC<hex4>,<payload>\r
//
// The payload is typically a TNC2 APRS packet built by BuildPositionPacket.
func WrapDPRS(payload string) string {
	crc := dprsCRC(payload)
	return fmt.Sprintf("$$CRC%04X,%s\r", crc, payload)
}

// ValidateDPRS parses a candidate DPRS sentence and returns the unwrapped
// payload if the framing and checksum are valid. Accepts sentences with or
// without a trailing CR/LF.
func ValidateDPRS(s string) (payload string, ok bool) {
	s = strings.TrimRight(s, "\r\n")
	if !strings.HasPrefix(s, "$$CRC") {
		return "", false
	}
	if len(s) < len("$$CRC")+4+1 { // need at least 4 hex + comma
		return "", false
	}
	comma := strings.IndexByte(s[len("$$CRC"):], ',')
	if comma < 0 {
		return "", false
	}
	hex := s[len("$$CRC") : len("$$CRC")+comma]
	body := s[len("$$CRC")+comma+1:]
	var want uint16
	if _, err := fmt.Sscanf(hex, "%X", &want); err != nil {
		return "", false
	}
	if dprsCRC(body) != want {
		return "", false
	}
	return body, true
}
