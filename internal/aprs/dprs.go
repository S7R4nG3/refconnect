package aprs

import (
	"fmt"
	"strings"
)

// dprsCRC computes the CRC field used in a DPRS "$$CRC" sentence.
// Algorithm: sum all bytes mod 65536. Many D-STAR gateways (ircDDBGateway,
// XLX, G4KLX) accept this simple 16-bit sum; it was chosen by Icom for
// its trivial implementation on the IC-9x00 series.
//
// Input is the text between the leading "," and the trailing "\r" —
// i.e. just the APRS TNC2 payload.
func dprsCRC(payload string) uint16 {
	var sum uint16
	for i := 0; i < len(payload); i++ {
		sum += uint16(payload[i])
	}
	return sum
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
