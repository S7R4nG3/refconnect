// Package dcs implements the DCS (D-STAR Connected System) reflector protocol.
// DCS operates over UDP port 30051.
//
// Unlike DPlus and DExtra, DCS embeds the D-STAR header in every voice packet.
// There are no separate header-only or voice-only DSVT packets.
//
// Connect packet (client → server, 519 bytes, per xlxd CDcsProtocol):
//
//	[0-7]    callsign, space-padded to 8 bytes
//	[8]      local module letter (repeat of callsign suffix)
//	[9]      target reflector module letter (A-Z)
//	[10]     0x00 (null terminator)
//	[11-18]  reflector callsign, space-padded to 8 bytes
//	[19-518] HTML info string (500 bytes, ignored by server)
//
// Connect ACK: 22-byte keepalive-format response confirming the link.
//
// Disconnect packet (19 bytes):
//
//	[0-7]   callsign, space-padded to 8 bytes
//	[8]     module letter
//	[9]     space (0x20)
//	[10-18] zero padding
//
// Keepalive (22 bytes):
//
//	[0-6]   reflector callsign (7 chars)
//	[7]     reflector module letter
//	[8]     space (0x20)
//	[9-15]  client callsign (7 chars)
//	[16]    client module letter
//	[17]    client module letter (repeated)
//	[18-21] tag {0x0A, 0x00, 0x20, 0x20}
//
// Voice/data packet (100 bytes, per xlxd CDcsProtocol):
//
//	[0-3]   tag "0001" (ASCII)
//	[4-42]  39-byte D-STAR header (flags + callsigns, NO CRC)
//	[43-44] stream ID (uint16, little-endian)
//	[45]    packet sequence (0-20, bit 0x40 = last frame)
//	[46-54] AMBE voice data (9 bytes)
//	[55-57] slow data (3 bytes)
//	[58-60] 3-byte absolute sequence counter (little-endian)
//	[61]    0x01 filler
//	[62-99] padding (zeros)
package dcs

import (
	"encoding/binary"
	"sync/atomic"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// DefaultPort is the standard UDP port for DCS reflectors.
const DefaultPort = 30051

// headerNoCRC is the size of a D-STAR header without the 2-byte CRC.
const headerNoCRC = dstar.HeaderBytes - 2 // 39

// Packet sizes.
const (
	connectPacketLen    = 519
	disconnectPacketLen = 19
	keepalivePacketLen  = 22
	voicePacketLen      = 100
	voiceTagLen         = 4
)

// voiceTag is the 4-byte ASCII prefix on DCS voice/data packets.
var voiceTag = [voiceTagLen]byte{'0', '0', '0', '1'}

// buildConnectPacket builds the 519-byte DCS link request.
// Bytes 0-6 are the callsign base (7 chars), byte 7 is the local module
// letter (same as DExtra), and byte 8 repeats the local module letter.
func buildConnectPacket(callsign string, localModule, targetModule byte, reflectorCall string) []byte {
	pkt := make([]byte, connectPacketLen)
	cs := dstar.PadCallsign(callsign, 8)
	copy(pkt[0:7], cs[:7])
	pkt[7] = localModule  // module letter at the 8th callsign position
	pkt[8] = localModule  // repeated
	pkt[9] = targetModule
	pkt[10] = 0x00
	copy(pkt[11:19], dstar.PadCallsign(reflectorCall, 8))
	// Bytes 19-518: HTML info string (optional, server ignores it).
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

// buildKeepalive returns the 22-byte DCS keepalive packet.
// Format: reflectorCall(7) + refModule(1) + space(1) + clientCall(7) +
// clientModule(1) + clientModule(1) + tag{0x0A, 0x00, 0x20, 0x20}.
func buildKeepalive(clientCall string, clientModule byte, reflectorCall string, reflectorModule byte) []byte {
	pkt := make([]byte, keepalivePacketLen)
	rs := dstar.PadCallsign(reflectorCall, 8)
	copy(pkt[0:7], rs[:7])
	pkt[7] = reflectorModule
	pkt[8] = ' '
	cs := dstar.PadCallsign(clientCall, 8)
	copy(pkt[9:16], cs[:7])
	pkt[16] = clientModule
	pkt[17] = clientModule
	pkt[18] = 0x0A
	pkt[19] = 0x00
	pkt[20] = ' '
	pkt[21] = ' '
	return pkt
}

// encodeVoicePacket builds a 100-byte DCS voice packet with embedded header.
// The header is 39 bytes (no CRC), followed by stream ID and voice data.
func encodeVoicePacket(streamID uint16, seq uint8, end bool, hdr dstar.DVHeader, f dstar.DVFrame, txSeq uint32) ([]byte, error) {
	raw, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, voicePacketLen)
	copy(pkt[0:4], voiceTag[:])
	copy(pkt[4:43], raw[:headerNoCRC]) // 39 bytes, skip CRC
	binary.LittleEndian.PutUint16(pkt[43:45], streamID)
	s := seq
	if end {
		s |= 0x40
	}
	pkt[45] = s
	copy(pkt[46:55], f.AMBE[:])
	copy(pkt[55:58], f.SlowData[:])
	// 3-byte absolute sequence counter (little-endian), per xlxd CDcsProtocol.
	pkt[58] = byte(txSeq)
	pkt[59] = byte(txSeq >> 8)
	pkt[60] = byte(txSeq >> 16)
	pkt[61] = 0x01 // filler byte, per xlxd
	// bytes 62-99 remain zero (padding)
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

	// Decode the 39-byte D-STAR header at bytes 4-42 (no CRC).
	// We build a full 41-byte buffer for DecodeHeaderNoCRC; the last 2 bytes
	// (CRC position) are left zero and ignored.
	var raw [dstar.HeaderBytes]byte
	copy(raw[:headerNoCRC], data[4:43])
	h := dstar.DecodeHeaderNoCRC(raw)

	streamID := binary.LittleEndian.Uint16(data[43:45])

	seq := data[45]
	end := seq&0x40 != 0
	seq &^= 0x40

	var f dstar.DVFrame
	f.Seq = seq
	f.End = end
	copy(f.AMBE[:], data[46:55])
	copy(f.SlowData[:], data[55:58])

	return &h, &f, streamID, nil
}

var streamCounter atomic.Uint32

func nextStreamID() uint16 {
	return uint16(streamCounter.Add(1))
}
