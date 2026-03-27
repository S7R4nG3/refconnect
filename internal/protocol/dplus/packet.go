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

// encodeHeader wraps a D-STAR header in a length-prefixed DPlus/DSVT packet.
func encodeHeader(streamID [4]byte, hdr dstar.DVHeader) ([]byte, error) {
	raw, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, 56) // 2-byte length prefix + 54-byte DSVT header
	binary.LittleEndian.PutUint16(pkt[0:2], 56)
	copy(pkt[2:6], dsvtMagic[:])
	pkt[6] = typeHeader
	pkt[7] = 0x00
	pkt[8] = 0x00
	pkt[9] = 0x03
	copy(pkt[10:14], streamID[:])
	pkt[14] = 0x80
	copy(pkt[15:56], raw[:])
	return pkt, nil
}

// encodeVoice wraps a DV voice frame in a length-prefixed DPlus/DSVT packet.
func encodeVoice(streamID [4]byte, f dstar.DVFrame) []byte {
	pkt := make([]byte, 29) // 2-byte length prefix + 27-byte DSVT voice frame
	binary.LittleEndian.PutUint16(pkt[0:2], 29)
	copy(pkt[2:6], dsvtMagic[:])
	pkt[6] = typeVoice
	pkt[7] = 0x00
	pkt[8] = 0x00
	pkt[9] = 0x03
	copy(pkt[10:14], streamID[:])
	seq := f.Seq
	if f.End {
		seq |= 0x40
	}
	pkt[14] = seq
	copy(pkt[15:24], f.AMBE[:])
	copy(pkt[24:27], f.SlowData[:])
	return pkt
}

// parsePacket decodes a received DPlus UDP payload (after stripping the 2-byte length prefix).
func parsePacket(data []byte) (*dstar.DVHeader, *dstar.DVFrame, error) {
	if len(data) < 13 {
		return nil, nil, nil // too short (keepalive echo, etc.)
	}
	if data[0] != 'D' || data[1] != 'S' || data[2] != 'V' || data[3] != 'T' {
		return nil, nil, nil // not a DSVT frame
	}

	switch data[4] {
	case typeHeader:
		if len(data) < 54 {
			return nil, nil, fmt.Errorf("dplus: short header packet")
		}
		var raw [dstar.HeaderBytes]byte
		copy(raw[:], data[13:54])
		h, err := dstar.DecodeHeader(raw)
		if err != nil {
			return nil, nil, err
		}
		return &h, nil, nil

	case typeVoice:
		if len(data) < 25 {
			return nil, nil, fmt.Errorf("dplus: short voice packet")
		}
		seq := data[12]
		end := seq&0x40 != 0
		seq &^= 0x40
		var f dstar.DVFrame
		f.Seq = seq
		f.End = end
		copy(f.AMBE[:], data[13:22])
		copy(f.SlowData[:], data[22:25])
		return nil, &f, nil
	}
	return nil, nil, nil
}

var streamCounter atomic.Uint32

func nextStreamID() [4]byte {
	var id [4]byte
	binary.LittleEndian.PutUint32(id[:], streamCounter.Add(1))
	return id
}
