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
// Disconnect packet (19 bytes, per ircDDBGateway CT_UNLINK):
//
//	[0-6]   client callsign (7 chars, space-padded)
//	[7]     space (0x20)
//	[8]     client module letter (must match connect registration)
//	[9]     space (0x20) — disconnect indicator
//	[10]    null (0x00)
//	[11-18] reflector callsign (8 chars, space-padded)
//
// Client poll / keepalive (17 bytes, per ircDDBGateway DCSHandler):
//
//	[0-7]   client callsign (8 bytes, space-padded, with module at [7])
//	[8]     0x00 (null separator)
//	[9-16]  reflector callsign (8 bytes, space-padded)
//
// NOTE: xlxd's IsValidKeepAlivePacket extracts the callsign from bytes 0-7.
// Sending a 22-byte packet with the reflector callsign at bytes 0-7 causes
// the server to fail the client lookup and silently ignore the keepalive.
// The 17-byte format puts the CLIENT callsign first, matching the server's
// expectation.
//
// Server keepalive (22 bytes, server → client):
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
	connectPacketLen       = 519
	disconnectShortLen     = 11
	disconnectLongLen      = 19
	pollPacketLen          = 17
	voicePacketLen         = 100
	voiceTagLen            = 4
)

// voiceTag is the 4-byte ASCII prefix on DCS voice/data packets.
var voiceTag = [voiceTagLen]byte{'0', '0', '0', '1'}

// clientInfoHTML is placed in the 500-byte HTML info field (bytes 19-518) of
// the DCS connect packet. XLX/DCS reflector dashboards display this to
// identify the connected client software.
var clientInfoHTML = []byte("<img src=\"https://github.com/S7R4nG3/refconnect/raw/main/docs/antenna-icon.png\">  <a href=\"https://github.com/S7R4nG3/refconnect\"><b>RefConnect</b></a> -  A DStar client")

// buildConnectPacket builds the 519-byte DCS link request.
// Bytes 0-6 are the callsign base (7 chars), byte 7 is the local module
// letter (same as DExtra), and byte 8 repeats the local module letter.
func buildConnectPacket(callsign string, localModule, targetModule byte, reflectorCall string) []byte {
	pkt := make([]byte, connectPacketLen)
	cs := dstar.PadCallsign(callsign, 8)
	copy(pkt[0:8], cs[:8])  // full 8-char callsign (space-padded)
	pkt[8] = localModule   // module letter in the protocol field
	pkt[9] = targetModule
	pkt[10] = 0x00
	copy(pkt[11:19], dstar.PadCallsign(reflectorCall, 8))
	// Bytes 19-518: HTML info field — identifies the client software on the
	// reflector dashboard. Dongle apps (DV Connect, BlueDV) populate this so
	// the dashboard can display the client name alongside the callsign.
	copy(pkt[19:], clientInfoHTML)
	return pkt
}

// buildDisconnectPackets returns both short (11-byte) and long (19-byte)
// DCS unlink packets. Different server implementations accept different
// sizes — xlxd accepts 11 or 19, other DCS servers may only accept one.
// Both formats mirror the connect packet structure with byte 9 changed
// from the target module to a space (disconnect indicator):
//
//	[0-7]   client callsign (8 chars, space-padded)
//	[8]     client module letter (same as connect byte 8)
//	[9]     space (0x20) — disconnect indicator (connect has target module here)
//	[10]    null (0x00)
//	[11-18] reflector callsign (8 chars, space-padded) — long format only
func buildDisconnectPackets(callsign string, clientModule byte, reflectorCall string) (short []byte, long []byte) {
	// Build the long (19-byte) packet first.
	long = make([]byte, disconnectLongLen)
	cs := dstar.PadCallsign(callsign, 8)
	copy(long[0:8], cs[:8])
	long[8] = clientModule
	long[9] = ' '
	long[10] = 0x00
	copy(long[11:19], dstar.PadCallsign(reflectorCall, 8))
	// Short (11-byte) is just the first 11 bytes.
	short = make([]byte, disconnectShortLen)
	copy(short, long[:disconnectShortLen])
	return short, long
}

// buildPoll returns the 17-byte DCS client keepalive/poll packet.
// Format: clientCall(7) + clientModule(1) + 0x00 + reflectorCall(8).
// This matches the ircDDBGateway DCSHandler implementation. xlxd's
// IsValidKeepAlivePacket extracts bytes 0-7 as the client callsign for
// peer lookup, so the client callsign MUST be at the start of the packet.
func buildPoll(clientCall string, clientModule byte, reflectorCall string) []byte {
	pkt := make([]byte, pollPacketLen)
	cs := dstar.PadCallsign(clientCall, 8)
	copy(pkt[0:7], cs[:7])
	pkt[7] = clientModule
	pkt[8] = 0x00
	copy(pkt[9:17], dstar.PadCallsign(reflectorCall, 8))
	return pkt
}

// encodeVoicePacket builds a 100-byte DCS voice packet with embedded header.
// rawHdr is the pre-encoded 41-byte D-STAR header (only the first 39 bytes are
// used — the CRC is omitted). Callers should encode the header once per
// transmission and pass the cached bytes to avoid re-encoding every frame.
func encodeVoicePacket(streamID uint16, seq uint8, end bool, rawHdr [dstar.HeaderBytes]byte, f dstar.DVFrame, txSeq uint32) []byte {
	pkt := make([]byte, voicePacketLen)
	copy(pkt[0:4], voiceTag[:])
	copy(pkt[4:43], rawHdr[:headerNoCRC]) // 39 bytes, skip CRC
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
	return pkt
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
