// Package dextra implements the DExtra linking protocol used by XRF reflectors.
// DExtra operates over UDP port 30001.
//
// Connect packet (client → server, 11 bytes, per G4KLX ircddbGateway ConnectData):
//
//	[0-7]  repeater callsign, space-padded to 8 bytes (byte[7] = local module, 'G' for gateway)
//	[8]    local module letter (copy of byte[7])
//	[9]    target reflector module letter — must NOT be ' '
//	[10]   0x00
//
// ACK (server → client, 14 bytes): first 10 bytes echo + "ACK\0"
// Disconnect: same 11-byte layout but byte[9] = ' ' (0x20)
// Keepalive: callsign space-padded to 8 bytes + 0x00 = 9 bytes (both directions)
package dextra

import (
	"encoding/binary"
	"fmt"
	"sync/atomic"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// UDP port used by DExtra reflectors.
const DefaultPort = 30001

// DSVT magic bytes that begin every DExtra DSVT voice/header packet.
var dsvtMagic = [4]byte{'D', 'S', 'V', 'T'}

// DSVT frame type bytes.
const (
	typeHeader = 0x10
	typeVoice  = 0x20
)

// buildConnectPacket builds the 11-byte DExtra link request packet.
// Wire layout (per G4KLX ircddbGateway ConnectData::getDExtraData):
//
//	[0-6]  callsign, space-padded to 7 bytes
//	[7]    local module letter ('G' for gateway/software client)
//	[8]    local module letter (copy of [7])
//	[9]    target reflector module letter
//	[10]   0x00
func buildConnectPacket(callsign string, module byte) []byte {
	pkt := make([]byte, 11)
	cs := dstar.PadCallsign(callsign, 7)
	copy(pkt[0:7], cs)
	pkt[7] = 'G' // local module: gateway indicator
	pkt[8] = 'G' // copy of local module
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

// encodeHeader builds a 54-byte DSVT header packet ready to send over UDP.
func encodeHeader(streamID [4]byte, hdr dstar.DVHeader) ([]byte, error) {
	raw, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	pkt := make([]byte, 54)
	copy(pkt[0:4], dsvtMagic[:])
	pkt[4] = typeHeader
	pkt[5] = 0x00
	pkt[6] = 0x00
	pkt[7] = 0x00
	copy(pkt[8:12], streamID[:])
	pkt[12] = 0x80 // header sequence marker
	copy(pkt[13:54], raw[:])
	return pkt, nil
}

// encodeVoice builds a 27-byte DSVT voice packet.
func encodeVoice(streamID [4]byte, f dstar.DVFrame) []byte {
	pkt := make([]byte, 27)
	copy(pkt[0:4], dsvtMagic[:])
	pkt[4] = typeVoice
	pkt[5] = 0x00
	pkt[6] = 0x00
	pkt[7] = 0x00
	copy(pkt[8:12], streamID[:])
	seq := f.Seq
	if f.End {
		seq |= 0x40
	}
	pkt[12] = seq
	copy(pkt[13:22], f.AMBE[:])
	copy(pkt[22:25], f.SlowData[:])
	return pkt
}

// parsePacket attempts to decode a received UDP payload.
// Returns (header, nil, nil), (nil, frame, nil), or (nil, nil, err).
// Non-DSVT packets (keepalives, control) are silently ignored.
func parsePacket(data []byte) (*dstar.DVHeader, *dstar.DVFrame, error) {
	if len(data) < 13 {
		return nil, nil, nil // too short — keepalive or control packet
	}
	if data[0] != 'D' || data[1] != 'S' || data[2] != 'V' || data[3] != 'T' {
		return nil, nil, nil // not a DSVT frame — silently ignore
	}

	switch data[4] {
	case typeHeader:
		if len(data) < 54 {
			return nil, nil, fmt.Errorf("dextra: short header packet (%d bytes)", len(data))
		}
		var raw [dstar.HeaderBytes]byte
		copy(raw[:], data[13:54])
		h, err := dstar.DecodeHeader(raw)
		if err != nil {
			return nil, nil, err
		}
		return &h, nil, nil

	case typeVoice:
		if len(data) < 27 {
			return nil, nil, fmt.Errorf("dextra: short voice packet (%d bytes)", len(data))
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
