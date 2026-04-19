// Package dcs implements the DCS (D-STAR Connected System) reflector protocol.
// DCS operates over UDP port 30051.
//
// Unlike DPlus and DExtra, DCS embeds the full 41-byte D-STAR header in every
// voice packet. There are no separate header-only or voice-only DSVT packets.
//
// Connect packet (client → server, 519 bytes):
//
//	[0-7]    callsign, space-padded to 8 bytes
//	[8]      local module letter
//	[9]      target reflector module letter (A-Z)
//	[10-518] zero padding
//
// Connect ACK: server echoes a packet of the same length.
// Connect NAK: shorter response or different content.
//
// Disconnect packet (19 bytes):
//
//	[0-7]   callsign, space-padded to 8 bytes
//	[8]     module letter
//	[9]     space (0x20)
//	[10-18] zero padding
//
// Keepalive (17 bytes, per ircDDBGateway DCSHandler):
//
//	[0-6]   client callsign (7 chars)
//	[7]     client module letter
//	[8]     0x00
//	[9-15]  reflector callsign (7 chars)
//	[16]    reflector module letter
//
// Voice/data packet (100 bytes):
//
//	[0-3]   tag "0001" (ASCII)
//	[4-44]  41-byte D-STAR header (flags + callsigns + CRC)
//	[45-46] stream ID (uint16, little-endian)
//	[47]    packet sequence (0-20, bit 0x40 = last frame)
//	[48-56] AMBE voice data (9 bytes)
//	[57-59] slow data (3 bytes)
//	[60-99] padding (zeros)
package dcs

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// DefaultPort is the standard UDP port for DCS reflectors.
const DefaultPort = 30051

// Packet sizes.
const (
	connectPacketLen    = 519
	disconnectPacketLen = 19
	keepalivePacketLen  = 17
	voicePacketLen      = 100
	voiceTagLen         = 4
)

// voiceTag is the 4-byte ASCII prefix on DCS voice/data packets.
var voiceTag = [voiceTagLen]byte{'0', '0', '0', '1'}

// buildConnectPacket builds the 519-byte DCS link request.
func buildConnectPacket(callsign string, localModule, targetModule byte) []byte {
	pkt := make([]byte, connectPacketLen)
	copy(pkt[0:8], dstar.PadCallsign(callsign, 8))
	pkt[8] = localModule
	pkt[9] = targetModule
	return pkt
}

// buildDisconnectPacket builds the 19-byte DCS unlink packet.
func buildDisconnectPacket(callsign string, module byte) []byte {
	pkt := make([]byte, disconnectPacketLen)
	copy(pkt[0:8], dstar.PadCallsign(callsign, 8))
	pkt[8] = module
	pkt[9] = ' '
	return pkt
}

// buildKeepalive returns the 17-byte DCS keepalive packet.
func buildKeepalive(clientCall string, clientModule byte, reflectorCall string, reflectorModule byte) []byte {
	pkt := make([]byte, keepalivePacketLen)
	cs := dstar.PadCallsign(clientCall, 8)
	copy(pkt[0:7], cs[:7])
	pkt[7] = clientModule
	pkt[8] = 0x00
	rs := dstar.PadCallsign(reflectorCall, 8)
	copy(pkt[9:16], rs[:7])
	pkt[16] = reflectorModule
	return pkt
}

// encodeVoicePacket builds a 100-byte DCS voice packet with embedded header.
func encodeVoicePacket(streamID uint16, seq uint8, end bool, hdr dstar.DVHeader, f dstar.DVFrame) ([]byte, error) {
	raw, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, voicePacketLen)
	copy(pkt[0:4], voiceTag[:])
	copy(pkt[4:45], raw[:])
	binary.LittleEndian.PutUint16(pkt[45:47], streamID)
	s := seq
	if end {
		s |= 0x40
	}
	pkt[47] = s
	copy(pkt[48:57], f.AMBE[:])
	copy(pkt[57:60], f.SlowData[:])
	// bytes 60-99 remain zero (padding)
	return pkt, nil
}

// parsePacket attempts to decode a received DCS UDP payload.
//
// For voice packets it returns both a header and a frame (both non-nil) plus
// the stream ID. For keepalive, connect ACK, and other control packets it
// returns (nil, nil, 0, nil).
func parsePacket(data []byte) (*dstar.DVHeader, *dstar.DVFrame, uint16, error) {
	if len(data) < 60 {
		return nil, nil, 0, nil // control/keepalive packet — silently ignore
	}
	// Check for voice tag "0001".
	if data[0] != '0' || data[1] != '0' || data[2] != '0' || data[3] != '1' {
		return nil, nil, 0, nil
	}

	// Decode embedded D-STAR header at bytes 4-44.
	var raw [dstar.HeaderBytes]byte
	copy(raw[:], data[4:45])
	h, err := dstar.DecodeHeader(raw)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("dcs: bad embedded header: %w", err)
	}

	streamID := binary.LittleEndian.Uint16(data[45:47])

	seq := data[47]
	end := seq&0x40 != 0
	seq &^= 0x40

	var f dstar.DVFrame
	f.Seq = seq
	f.End = end
	copy(f.AMBE[:], data[48:57])
	copy(f.SlowData[:], data[57:60])

	return &h, &f, streamID, nil
}

var streamCounter atomic.Uint32

func nextStreamID() uint16 {
	return uint16(streamCounter.Add(1))
}
