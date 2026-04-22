package dstar

import (
	"encoding/binary"
	"fmt"
	"strings"
)

// EncodeHeader serialises a DVHeader into the 41-byte wire representation.
// Callsign fields are space-padded to their fixed widths.
// The CRC covers the first 39 bytes and is stored little-endian in bytes 39-40.
func EncodeHeader(h DVHeader) ([HeaderBytes]byte, error) {
	var buf [HeaderBytes]byte

	buf[0] = h.Flag1
	buf[1] = h.Flag2
	buf[2] = h.Flag3

	fields := []struct {
		val string
		n   int
		off int
	}{
		{h.RPT2, 8, 3},
		{h.RPT1, 8, 11},
		{h.YourCall, 8, 19},
		{h.MyCall, 8, 27},
		{h.MyCallSuffix, 4, 35},
	}
	for _, f := range fields {
		padded := PadCallsign(strings.ToUpper(f.val), f.n)
		copy(buf[f.off:f.off+f.n], padded)
	}

	crc := crc16CCITT(buf[:39])
	binary.LittleEndian.PutUint16(buf[39:41], crc)
	return buf, nil
}

// DecodeHeader parses a 41-byte wire header into a DVHeader.
// Returns an error if the CRC does not match.
func DecodeHeader(raw [HeaderBytes]byte) (DVHeader, error) {
	crc := crc16CCITT(raw[:39])
	stored := binary.LittleEndian.Uint16(raw[39:41])
	if crc != stored {
		return DVHeader{}, fmt.Errorf("dstar: header CRC mismatch (got %04X, want %04X)", stored, crc)
	}
	return decodeHeaderFields(raw), nil
}

// DecodeHeaderNoCRC parses a 41-byte wire header without validating the CRC.
// Used by protocols like DCS where the reflector may regenerate headers with
// a different CRC algorithm.
func DecodeHeaderNoCRC(raw [HeaderBytes]byte) DVHeader {
	return decodeHeaderFields(raw)
}

func decodeHeaderFields(raw [HeaderBytes]byte) DVHeader {
	return DVHeader{
		Flag1:        raw[0],
		Flag2:        raw[1],
		Flag3:        raw[2],
		RPT2:         string(raw[3:11]),
		RPT1:         string(raw[11:19]),
		YourCall:     string(raw[19:27]),
		MyCall:       string(raw[27:35]),
		MyCallSuffix: string(raw[35:39]),
	}
}
