// Package dextra implements the DExtra linking protocol used by XRF reflectors.
// DExtra operates over UDP port 30001.
//
// Connect packet (client → server, 11 bytes, per G4KLX ircddbGateway ConnectData):
//
//	[0-6]  callsign base, space-padded to 7 bytes
//	[7]    local module letter (8th character of MyCall — e.g. ' ', 'A'–'Z')
//	[8]    local module letter (copy of [7])
//	[9]    target reflector module letter — must NOT be ' '
//	[10]   0x00
//
// ACK (server → client, 14 bytes): first 10 bytes echo + "ACK\0"
// Disconnect: same 11-byte layout but byte[9] = ' ' (0x20)
// Keepalive: callsign space-padded to 8 bytes + 0x00 = 9 bytes (both directions)
//
// DSVT data framing:
// Modern XRF reflectors (openquad.net, etc.) run xlxd, which expects the same
// DSVT layout as the XLX native protocol: a 12-byte fixed tag (DSVT magic +
// type + 3 zeros + 4 config/band bytes), then a 2-byte LE stream ID, then the
// sequence byte and payload.
//
//	Header: 56 bytes = 12-byte tag + 2 stream ID + 1 seq(0x80) + 41 D-STAR header
//	Voice:  27 bytes = 12-byte tag + 2 stream ID + 1 seq       + 9 AMBE + 3 SlowData
package dextra

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// UDP port used by DExtra reflectors.
const DefaultPort = 30001

// DSVT 12-byte fixed tag prefixes for header and voice frames.
// These match the xlxd DSVT framing used by modern XRF reflectors.
var dsvtHeaderTag = [12]byte{'D', 'S', 'V', 'T', 0x10, 0x00, 0x00, 0x00, 0x20, 0x00, 0x01, 0x01}
var dsvtVoiceTag = [12]byte{'D', 'S', 'V', 'T', 0x20, 0x00, 0x00, 0x00, 0x20, 0x00, 0x01, 0x01}

// DSVT frame type byte (at tag offset 4).
const (
	typeHeader = 0x10
	typeVoice  = 0x20
)

// Packet sizes.
const (
	headerPacketLen = 56 // 12 tag + 2 streamID + 1 seq + 41 header
	voicePacketLen  = 27 // 12 tag + 2 streamID + 1 seq + 9 AMBE + 3 SlowData
)

// buildConnectPacket builds the 11-byte DExtra link request packet.
// Wire layout (per G4KLX ircddbGateway ConnectData::getDExtraData):
//
//	[0-6]  callsign base, space-padded to 7 bytes
//	[7]    local module letter (8th character of MyCall — e.g. ' ', 'A'–'Z')
//	[8]    local module letter (copy of [7])
//	[9]    target reflector module letter
//	[10]   0x00
//
// callsign is the full 8-char MyCall (e.g. "KR4GCQ  " or "KR4GCQ D"); the
// 8th character carries the local module/suffix chosen in the UI.
func buildConnectPacket(callsign string, module byte) []byte {
	pkt := make([]byte, 11)
	cs := dstar.PadCallsign(callsign, 8) // ensure 8 chars
	copy(pkt[0:7], cs)
	localMod := cs[7] // suffix: space or 'A'–'Z'
	pkt[7] = localMod
	pkt[8] = localMod // copy of local module
	pkt[9] = module
	pkt[10] = 0x00
	return pkt
}

// buildDisconnectPacket builds the 11-byte DExtra unlink packet.
// Same as connect but byte[9] = ' ' to signal disconnect.
func buildDisconnectPacket(callsign string) []byte {
	pkt := buildConnectPacket(callsign, ' ')
	pkt[9] = ' '
	return pkt
}

// buildKeepalive returns the 9-byte DExtra keepalive packet.
// Wire layout: callsign space-padded to 8 bytes + 0x00.
func buildKeepalive(callsign string) []byte {
	pkt := make([]byte, 9)
	copy(pkt[0:8], dstar.PadCallsign(callsign, 8))
	pkt[8] = 0x00
	return pkt
}

// encodeHeader builds a 56-byte DSVT header packet ready to send over UDP.
// Layout: 12-byte tag + streamID(2, LE) + 0x80 + dstar_header(41) = 56 bytes.
func encodeHeader(streamID uint16, hdr dstar.DVHeader) ([]byte, error) {
	raw, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, headerPacketLen)
	copy(pkt[0:12], dsvtHeaderTag[:])
	binary.LittleEndian.PutUint16(pkt[12:14], streamID)
	pkt[14] = 0x80 // header sequence marker
	copy(pkt[15:56], raw[:])
	return pkt, nil
}

// encodeVoice builds a 27-byte DSVT voice packet.
// Layout: 12-byte tag + streamID(2, LE) + seq(1) + AMBE(9) + SlowData(3) = 27 bytes.
func encodeVoice(streamID uint16, f dstar.DVFrame) []byte {
	pkt := make([]byte, voicePacketLen)
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

// parsePacket attempts to decode a received UDP payload.
// Returns (header, nil, nil), (nil, frame, nil), or (nil, nil, err).
// Non-DSVT packets (keepalives, control) are silently ignored.
func parsePacket(data []byte) (*dstar.DVHeader, *dstar.DVFrame, error) {
	if len(data) < 15 {
		return nil, nil, nil // too short — keepalive or control packet
	}
	if data[0] != 'D' || data[1] != 'S' || data[2] != 'V' || data[3] != 'T' {
		return nil, nil, nil // not a DSVT frame — silently ignore
	}

	switch data[4] {
	case typeHeader:
		if len(data) < headerPacketLen {
			return nil, nil, fmt.Errorf("dextra: short header packet (%d bytes)", len(data))
		}
		var raw [dstar.HeaderBytes]byte
		copy(raw[:], data[15:56])
		h, err := dstar.DecodeHeader(raw)
		if err != nil {
			return nil, nil, err
		}
		return &h, nil, nil

	case typeVoice:
		if len(data) < voicePacketLen {
			return nil, nil, fmt.Errorf("dextra: short voice packet (%d bytes)", len(data))
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
