// Package xlx implements the XLX multi-protocol reflector client.
//
// XLX uses its own control protocol on port 30001 (per LX3JL xlxd cxlxprotocol.cpp).
// Voice data uses the DExtra DSVT framing that xlxd expects:
// 12-byte fixed tag, 2-byte little-endian stream ID, then header/voice payload.
//
// Connect packet (client → server, 39 bytes):
//
//	[0]     'L'
//	[1-8]   callsign (8 bytes, space-padded)
//	[9]     VERSION_MAJOR
//	[10]    VERSION_MINOR
//	[11]    VERSION_REVISION
//	[12-37] modules string (null-terminated, e.g. "A\0"), padded to fill
//	[38]    0x00 (validity marker)
//
// ACK (server → client, 39 bytes): same layout but byte[0] = 'A'
// NACK (server → client, 10 bytes): 'N' + callsign(8) + 0x00
// Disconnect: 'U' + callsign(8) + 0x00 = 10 bytes
// Keepalive: callsign(8) + 0x00 = 9 bytes (both directions)
package xlx

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// DefaultPort is the XLX control/DExtra port.
const DefaultPort = 30001

// xlxd DSVT 12-byte fixed tag prefixes for header and voice frames.
var dsvtHeaderTag = [12]byte{'D', 'S', 'V', 'T', 0x10, 0x00, 0x00, 0x00, 0x20, 0x00, 0x01, 0x02}
var dsvtVoiceTag = [12]byte{'D', 'S', 'V', 'T', 0x20, 0x00, 0x00, 0x00, 0x20, 0x00, 0x01, 0x02}

// Client version sent in connect packets. xlxd accepts any non-zero version.
const (
	versionMajor    = 1
	versionMinor    = 0
	versionRevision = 0
)

// buildConnectPacket builds the 39-byte XLX link request packet.
func buildConnectPacket(callsign string, module byte) []byte {
	pkt := make([]byte, 39) // zero-filled; pkt[38] = 0x00 validity marker
	pkt[0] = 'L'
	copy(pkt[1:9], dstar.PadCallsign(callsign, 8))
	pkt[9] = versionMajor
	pkt[10] = versionMinor
	pkt[11] = versionRevision
	pkt[12] = module     // modules string: single module letter
	pkt[13] = 0x00       // null-terminate the modules string
	// pkt[14-38] = 0x00 (already zero; pkt[38] is the required null terminator)
	return pkt
}

// buildDisconnectPacket builds the 10-byte XLX disconnect packet.
func buildDisconnectPacket(callsign string) []byte {
	pkt := make([]byte, 10)
	pkt[0] = 'U'
	copy(pkt[1:9], dstar.PadCallsign(callsign, 8))
	pkt[9] = 0x00
	return pkt
}

// buildKeepalive returns the 9-byte XLX keepalive packet (callsign + 0x00).
func buildKeepalive(callsign string) []byte {
	pkt := make([]byte, 9)
	copy(pkt[0:8], dstar.PadCallsign(callsign, 8))
	pkt[8] = 0x00
	return pkt
}

// encodeHeader wraps a D-STAR header in a 56-byte xlxd-compatible DSVT packet.
// Layout: 12-byte tag + streamID(2, LE) + 0x80 + dstar_header(41) = 56 bytes.
func encodeHeader(streamID uint16, hdr dstar.DVHeader) ([]byte, error) {
	raw, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, 56)
	copy(pkt[0:12], dsvtHeaderTag[:])
	binary.LittleEndian.PutUint16(pkt[12:14], streamID)
	pkt[14] = 0x80
	copy(pkt[15:56], raw[:])
	return pkt, nil
}

// encodeVoice wraps a DV voice frame in a 27-byte xlxd-compatible DSVT packet.
// Layout: 12-byte tag + streamID(2, LE) + seq(1) + AMBE(9) + slowdata(3) = 27 bytes.
func encodeVoice(streamID uint16, f dstar.DVFrame) []byte {
	pkt := make([]byte, 27)
	copy(pkt[0:12], dsvtVoiceTag[:])
	binary.LittleEndian.PutUint16(pkt[12:14], streamID)
	seq := f.Seq
	if f.End {
		seq |= 0x40
	}
	pkt[14] = seq
	copy(pkt[15:24], f.AMBE[:])
	copy(pkt[24:27], f.SlowData[:])
	return pkt
}

// parsePacket decodes a received xlxd DSVT UDP payload.
// Voice header: 56 bytes, tag[8]==0x20, type==0x10
// Voice frame:  27 bytes, tag[8]==0x20, type==0x20
// Non-DSVT packets (keepalives, control) are silently ignored.
func parsePacket(data []byte) (*dstar.DVHeader, *dstar.DVFrame, error) {
	if len(data) < 13 {
		return nil, nil, nil // keepalive or control packet
	}
	if data[0] != 'D' || data[1] != 'S' || data[2] != 'V' || data[3] != 'T' {
		return nil, nil, nil // silently ignore non-DSVT packets
	}

	switch data[4] {
	case 0x10: // header
		if len(data) < 56 {
			return nil, nil, fmt.Errorf("xlx: short header packet (%d bytes)", len(data))
		}
		var raw [dstar.HeaderBytes]byte
		copy(raw[:], data[15:56])
		h, err := dstar.DecodeHeader(raw)
		if err != nil {
			return nil, nil, err
		}
		return &h, nil, nil

	case 0x20: // voice
		if len(data) < 27 {
			return nil, nil, fmt.Errorf("xlx: short voice packet (%d bytes)", len(data))
		}
		seq := data[14]
		end := seq&0x40 != 0
		seq &^= 0x40
		var f dstar.DVFrame
		f.Seq = seq
		f.End = end
		copy(f.AMBE[:], data[15:24])
		copy(f.SlowData[:], data[24:27])
		return nil, &f, nil
	}
	return nil, nil, nil
}

var streamCounter atomic.Uint32

func nextStreamID() uint16 {
	return uint16(streamCounter.Add(1))
}
