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

	// Pad callsign fields directly into the output buffer to avoid
	// intermediate allocations from PadCallsign.
	padInto(buf[3:11], strings.ToUpper(h.RPT2))
	padInto(buf[11:19], strings.ToUpper(h.RPT1))
	padInto(buf[19:27], strings.ToUpper(h.YourCall))
	padInto(buf[27:35], strings.ToUpper(h.MyCall))
	padInto(buf[35:39], strings.ToUpper(h.MyCallSuffix))

	crc := crc16CCITT(buf[:39])
	binary.LittleEndian.PutUint16(buf[39:41], crc)
	return buf, nil
}

// padInto copies src into dst, space-padding any remaining bytes.
// No allocations — writes directly into the destination slice.
func padInto(dst []byte, src string) {
	n := copy(dst, src)
	for i := n; i < len(dst); i++ {
		dst[i] = ' '
	}
}

// CRCError reports a header whose stored CRC doesn't match its contents.
type CRCError struct {
	Got, Want uint16
}

func (e *CRCError) Error() string {
	return fmt.Sprintf("dstar: header CRC mismatch (got %04X, want %04X)", e.Got, e.Want)
}

// DecodeHeader parses a 41-byte wire header into a DVHeader.
// Returns a *CRCError if the CRC does not match.
func DecodeHeader(raw [HeaderBytes]byte) (DVHeader, error) {
	crc := crc16CCITT(raw[:39])
	stored := binary.LittleEndian.Uint16(raw[39:41])
	if crc != stored {
		return DVHeader{}, &CRCError{Got: stored, Want: crc}
	}
	return decodeHeaderFields(raw), nil
}

// DecodeNetworkHeader parses a 41-byte header received from a reflector.
// It always returns the decoded header. A wrong CRC is reported as a
// *CRCError alongside it rather than rejecting the header: reflectors relay
// headers whose CRC was computed wrongly by the originating radio or hotspot,
// UDP's own checksum already guards the packet against corruption, and the
// radio paths re-encode the header with a fresh CRC. The placeholder values
// 0x0000 and 0xFFFF (xlxd forwards headers without computing the CRC) count
// as valid.
func DecodeNetworkHeader(raw [HeaderBytes]byte) (DVHeader, error) {
	h := decodeHeaderFields(raw)
	stored := binary.LittleEndian.Uint16(raw[39:41])
	if stored == 0x0000 || stored == 0xFFFF {
		return h, nil
	}
	if crc := crc16CCITT(raw[:39]); crc != stored {
		return h, &CRCError{Got: stored, Want: crc}
	}
	return h, nil
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
