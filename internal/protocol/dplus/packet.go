// Package dplus implements the DPlus linking protocol used by REF reflectors.
// DPlus operates over UDP port 20001.
//
// Connection is a two-step handshake (per G4KLX ircddbGateway DPlusHandler):
//
//  Step 1 — client sends CT_LINK1 (5 bytes): 05 00 18 00 01
//            server echoes it back.
//
//  Step 2 — client sends CT_LINK2 login (28 bytes):
//            1C C0 04 00 + callsign[16] + "DV019999"
//            server responds with CT_ACK (8 bytes):
//            08 C0 04 00 4F 4B 52 57  ("OKRW" at bytes [4-7])
//
// Keepalive: 3 bytes 03 60 00, sent every 1s in both directions.
// Disconnect: CT_LINK1 with byte[4]=0x00, i.e. 05 00 18 00 00 (sent twice).
package dplus

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// DefaultPort is the standard DPlus reflector port.
const DefaultPort = 20001

// DSVT magic.
var dsvtMagic = [4]byte{'D', 'S', 'V', 'T'}

const (
	typeHeader = 0x10
	typeVoice  = 0x20
)

// buildLink1Packet builds the 5-byte CT_LINK1 packet used in step 1 of the
// DPlus handshake (and as the disconnect packet).
// connect=true → 05 00 18 00 01  (connect)
// connect=false → 05 00 18 00 00 (disconnect)
func buildLink1Packet(connect bool) []byte {
	pkt := []byte{0x05, 0x00, 0x18, 0x00, 0x00}
	if connect {
		pkt[4] = 0x01
	}
	return pkt
}

// buildLoginPacket constructs the 28-byte CT_LINK2 DPlus login packet.
// Wire layout (per G4KLX ircddbGateway ConnectData::getDPlusData):
//
//	[0]     0x1C   — packet length (28)
//	[1]     0xC0   — DPlus control marker
//	[2]     0x04   — subtype: login
//	[3]     0x00
//	[4:12]  callsign — up to 8 bytes, null-padded to 16 bytes total ([4:20])
//	[20:28] "DV019999"
func buildLoginPacket(callsign string) []byte {
	pkt := make([]byte, 28) // zero-filled
	pkt[0] = 0x1C
	pkt[1] = 0xC0
	pkt[2] = 0x04
	pkt[3] = 0x00
	cs := []byte(callsign)
	if len(cs) > 8 {
		cs = cs[:8]
	}
	copy(pkt[4:], cs) // null-padded automatically by zero-filled slice
	copy(pkt[20:28], "DV019999")
	return pkt
}

// buildDisconnectPacket returns the 5-byte CT_UNLINK packet.
// ircDDBGateway sends this twice for reliability.
func buildDisconnectPacket() []byte {
	return buildLink1Packet(false)
}

// buildKeepalive returns the 3-byte DPlus keepalive packet (03 60 00).
func buildKeepalive() []byte {
	return []byte{0x03, 0x60, 0x00}
}

// DPlus DSVT packet framing (verified against REF001 live capture 2026-04-08):
//
// All DPlus packets use a 2-byte prefix:
//   [0] = total packet length (single byte)
//   [1] = packet class: 0x80 for DSVT data, 0xC0 for control, 0x60 for keepalive
//
// DSVT payload layout (after 2-byte prefix):
//   [2-5]   "DSVT" magic
//   [6]     Frame type: 0x10 (header) or 0x20 (voice)
//   [7-9]   0x00, 0x00, 0x00
//   [10-13] Config/band flags (0x20, 0x00, 0x01, 0x01)
//   [14-15] Stream ID (2 bytes, big-endian)
//   [16]    Sequence: 0x80 for header, 0-20 for voice (bit 6 = end)
//   [17+]   Payload (41-byte D-STAR header, or 12-byte voice frame)
//
// Header packet = 58 bytes total. Voice packet = 29 bytes total.

const (
	dplusDataMarker = 0x80
	headerPacketLen = 58 // 2 prefix + 4 DSVT + 1 type + 3 zeros + 4 config + 2 streamID + 1 seq + 41 header = 58
	voicePacketLen  = 29 // 2 prefix + 4 DSVT + 1 type + 3 zeros + 4 config + 2 streamID + 1 seq + 9 AMBE + 3 slow = 29
)

func encodeHeader(streamID [2]byte, hdr dstar.DVHeader) ([]byte, error) {
	raw, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, headerPacketLen)
	pkt[0] = headerPacketLen // 0x3A
	pkt[1] = dplusDataMarker // 0x80
	copy(pkt[2:6], dsvtMagic[:])
	pkt[6] = typeHeader
	// pkt[7..9] = 0x00 (already zero)
	pkt[10] = 0x20 // band/config flag
	pkt[11] = 0x00
	pkt[12] = 0x01
	pkt[13] = 0x01
	copy(pkt[14:16], streamID[:])
	pkt[16] = 0x80 // header sequence flag
	copy(pkt[17:58], raw[:])
	return pkt, nil
}

func encodeVoice(streamID [2]byte, f dstar.DVFrame) []byte {
	pkt := make([]byte, voicePacketLen)
	pkt[0] = voicePacketLen // 0x1D
	pkt[1] = dplusDataMarker // 0x80
	copy(pkt[2:6], dsvtMagic[:])
	pkt[6] = typeVoice
	// pkt[7..9] = 0x00 (already zero)
	pkt[10] = 0x20
	pkt[11] = 0x00
	pkt[12] = 0x01
	pkt[13] = 0x01
	copy(pkt[14:16], streamID[:])
	seq := f.Seq
	if f.End {
		seq |= 0x40
	}
	pkt[16] = seq
	copy(pkt[17:26], f.AMBE[:])
	copy(pkt[26:29], f.SlowData[:])
	return pkt
}

// parsePacket decodes a received DPlus DSVT packet.
// The raw UDP packet (including the 2-byte prefix) is passed in.
func parsePacket(buf []byte) (*dstar.DVHeader, *dstar.DVFrame, error) {
	if len(buf) < 17 {
		return nil, nil, nil
	}
	if buf[1] != dplusDataMarker {
		return nil, nil, nil
	}
	if buf[2] != 'D' || buf[3] != 'S' || buf[4] != 'V' || buf[5] != 'T' {
		return nil, nil, nil
	}

	switch buf[6] {
	case typeHeader:
		if len(buf) < 58 {
			return nil, nil, fmt.Errorf("dplus: short header packet (%d bytes)", len(buf))
		}
		// D-STAR header at buf[17:58] (41 bytes including CRC)
		var raw [dstar.HeaderBytes]byte
		copy(raw[:], buf[17:58])
		h, err := dstar.DecodeHeader(raw)
		if err != nil {
			return nil, nil, err
		}
		return &h, nil, nil

	case typeVoice:
		if len(buf) < 29 {
			return nil, nil, fmt.Errorf("dplus: short voice packet (%d bytes)", len(buf))
		}
		// buf[16] = seq, buf[17:26] = AMBE(9), buf[26:29] = SlowData(3)
		seq := buf[16]
		end := seq&0x40 != 0
		seq &^= 0x40
		var f dstar.DVFrame
		f.Seq = seq
		f.End = end
		copy(f.AMBE[:], buf[17:26])
		copy(f.SlowData[:], buf[26:29])
		return nil, &f, nil
	}
	return nil, nil, nil
}

var streamCounter atomic.Uint32

func nextStreamID() [2]byte {
	var id [2]byte
	binary.BigEndian.PutUint16(id[:], uint16(streamCounter.Add(1)))
	return id
}
