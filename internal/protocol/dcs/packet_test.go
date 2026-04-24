package dcs

import (
	"encoding/binary"
	"testing"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

func TestBuildConnectPacket(t *testing.T) {
	pkt := buildConnectPacket("KR4GCQ D", 'D', 'C', "DCS001  ")
	if len(pkt) != connectPacketLen {
		t.Fatalf("connect packet length = %d, want %d", len(pkt), connectPacketLen)
	}
	// Bytes 0-7: full callsign (space-padded), byte 8: module letter.
	if string(pkt[0:8]) != "KR4GCQ D" {
		t.Errorf("callsign = %q, want %q", string(pkt[0:8]), "KR4GCQ D")
	}
	if pkt[8] != 'D' {
		t.Errorf("byte[8] module = %c, want D", pkt[8])
	}
	if pkt[9] != 'C' {
		t.Errorf("target module = %c, want C", pkt[9])
	}
	if pkt[10] != 0x00 {
		t.Errorf("byte[10] = 0x%02X, want 0x00", pkt[10])
	}
	if string(pkt[11:19]) != "DCS001  " {
		t.Errorf("reflector callsign = %q, want %q", string(pkt[11:19]), "DCS001  ")
	}
	// Bytes 19+: HTML info field starts with the client identifier.
	info := string(pkt[19 : 19+len(clientInfoHTML)])
	if info != string(clientInfoHTML) {
		t.Errorf("HTML info = %q, want %q", info, string(clientInfoHTML))
	}
	// Remainder of the info field should be zero-padded.
	for i := 19 + len(clientInfoHTML); i < connectPacketLen; i++ {
		if pkt[i] != 0 {
			t.Errorf("byte[%d] = 0x%02X, want 0x00 (zero padding)", i, pkt[i])
			break
		}
	}
}

func TestBuildDisconnectPackets(t *testing.T) {
	short, long := buildDisconnectPackets("KR4GCQ D", 'G', "DCS001  ")

	// Short packet: 11 bytes.
	if len(short) != disconnectShortLen {
		t.Fatalf("short disconnect length = %d, want %d", len(short), disconnectShortLen)
	}

	// Long packet: 19 bytes.
	if len(long) != disconnectLongLen {
		t.Fatalf("long disconnect length = %d, want %d", len(long), disconnectLongLen)
	}

	// Both share the same first 11 bytes — test via the long packet.
	// Bytes 0-7: callsign (space-padded), byte 8: client module.
	if string(long[0:8]) != "KR4GCQ D" {
		t.Errorf("callsign = %q, want %q", string(long[0:8]), "KR4GCQ D")
	}
	if long[8] != 'G' {
		t.Errorf("byte[8] client module = 0x%02X, want 'G'", long[8])
	}
	// Byte 9: space (disconnect indicator).
	if long[9] != ' ' {
		t.Errorf("byte[9] = 0x%02X, want 0x20 (space)", long[9])
	}
	// Byte 10: null.
	if long[10] != 0x00 {
		t.Errorf("byte[10] = 0x%02X, want 0x00", long[10])
	}
	// Bytes 11-18: reflector callsign (long format only).
	if string(long[11:19]) != "DCS001  " {
		t.Errorf("reflector callsign = %q, want %q", string(long[11:19]), "DCS001  ")
	}
	// Short must equal the first 11 bytes of long.
	for i := 0; i < disconnectShortLen; i++ {
		if short[i] != long[i] {
			t.Errorf("short[%d] = 0x%02X, long[%d] = 0x%02X — mismatch", i, short[i], i, long[i])
		}
	}
}

func TestBuildPoll(t *testing.T) {
	pkt := buildPoll("KR4GCQ D", 'G', "DCS001  ")
	if len(pkt) != pollPacketLen {
		t.Fatalf("poll length = %d, want %d", len(pkt), pollPacketLen)
	}
	// Bytes 0-6: client callsign base (7 chars), byte 7: client module.
	if string(pkt[0:7]) != "KR4GCQ " {
		t.Errorf("client callsign = %q, want %q", string(pkt[0:7]), "KR4GCQ ")
	}
	if pkt[7] != 'G' {
		t.Errorf("client module = %c, want G", pkt[7])
	}
	if pkt[8] != 0x00 {
		t.Errorf("separator = 0x%02X, want 0x00", pkt[8])
	}
	// Bytes 9-16: reflector callsign (8 chars, space-padded).
	if string(pkt[9:17]) != "DCS001  " {
		t.Errorf("reflector callsign = %q, want %q", string(pkt[9:17]), "DCS001  ")
	}
}

func TestVoicePacketRoundTrip(t *testing.T) {
	hdr := dstar.DVHeader{
		Flag1:        0x00,
		Flag2:        0x00,
		Flag3:        0x00,
		RPT2:         "DCS001 C",
		RPT1:         "DCS001 G",
		YourCall:     "CQCQCQ  ",
		MyCall:       "KR4GCQ  ",
		MyCallSuffix: "    ",
	}
	frm := dstar.DVFrame{
		Seq:      5,
		AMBE:     [9]byte{0x9E, 0x8D, 0x32, 0x88, 0x26, 0x1A, 0x3F, 0x61, 0xE8},
		SlowData: [3]byte{0x55, 0x2D, 0x16},
		End:      false,
	}

	rawHdr, err := dstar.EncodeHeader(hdr)
	if err != nil {
		t.Fatalf("EncodeHeader: %v", err)
	}
	pkt := encodeVoicePacket(0x1234, 5, false, rawHdr, frm, 42)
	if len(pkt) != voicePacketLen {
		t.Fatalf("voice packet length = %d, want %d", len(pkt), voicePacketLen)
	}
	// Check tag.
	if string(pkt[0:4]) != "0001" {
		t.Errorf("tag = %q, want %q", string(pkt[0:4]), "0001")
	}
	// Header is 39 bytes at [4:43], no CRC.
	// Stream ID at bytes 43-44.
	gotSID := binary.LittleEndian.Uint16(pkt[43:45])
	if gotSID != 0x1234 {
		t.Errorf("stream ID = 0x%04X, want 0x1234", gotSID)
	}
	// Seq at byte 45.
	if pkt[45] != 5 {
		t.Errorf("seq byte = %d, want 5", pkt[45])
	}
	// AMBE at bytes 46-54.
	if pkt[46] != 0x9E || pkt[54] != 0xE8 {
		t.Errorf("AMBE not at expected offset 46-54")
	}
	// Slow data at bytes 55-57.
	if pkt[55] != 0x55 || pkt[56] != 0x2D || pkt[57] != 0x16 {
		t.Errorf("SlowData not at expected offset 55-57")
	}
	// TX sequence counter at bytes 58-60.
	if pkt[58] != 42 {
		t.Errorf("txSeq low = %d, want 42", pkt[58])
	}
	// Filler at byte 61.
	if pkt[61] != 0x01 {
		t.Errorf("filler = 0x%02X, want 0x01", pkt[61])
	}

	// Parse it back.
	gotHdr, gotFrm, parsedSID, err := parsePacket(pkt)
	if err != nil {
		t.Fatalf("parsePacket: %v", err)
	}
	if gotHdr == nil || gotFrm == nil {
		t.Fatal("parsePacket returned nil header or frame")
	}
	if parsedSID != 0x1234 {
		t.Errorf("parsed stream ID = 0x%04X, want 0x1234", parsedSID)
	}
	if gotHdr.MyCall != hdr.MyCall {
		t.Errorf("MyCall = %q, want %q", gotHdr.MyCall, hdr.MyCall)
	}
	if gotHdr.RPT2 != hdr.RPT2 {
		t.Errorf("RPT2 = %q, want %q", gotHdr.RPT2, hdr.RPT2)
	}
	if gotFrm.Seq != 5 {
		t.Errorf("seq = %d, want 5", gotFrm.Seq)
	}
	if gotFrm.End {
		t.Error("End = true, want false")
	}
	if gotFrm.AMBE != frm.AMBE {
		t.Errorf("AMBE mismatch")
	}
	if gotFrm.SlowData != frm.SlowData {
		t.Errorf("SlowData mismatch")
	}
}

func TestVoicePacketEndFlag(t *testing.T) {
	hdr := dstar.DVHeader{
		RPT2:         "DCS001 C",
		RPT1:         "DCS001 G",
		YourCall:     "CQCQCQ  ",
		MyCall:       "KR4GCQ  ",
		MyCallSuffix: "    ",
	}
	frm := dstar.DVFrame{
		Seq:  20,
		AMBE: dstar.SilenceAMBE,
		End:  true,
	}

	rawHdr, err := dstar.EncodeHeader(hdr)
	if err != nil {
		t.Fatalf("EncodeHeader: %v", err)
	}
	pkt := encodeVoicePacket(0xABCD, 20, true, rawHdr, frm, 0)

	_, gotFrm, _, err := parsePacket(pkt)
	if err != nil {
		t.Fatalf("parsePacket: %v", err)
	}
	if !gotFrm.End {
		t.Error("End = false, want true")
	}
	if gotFrm.Seq != 20 {
		t.Errorf("seq = %d, want 20", gotFrm.Seq)
	}
}

func TestParsePacketIgnoresShort(t *testing.T) {
	hdr, frm, sid, err := parsePacket(make([]byte, 17))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hdr != nil || frm != nil || sid != 0 {
		t.Error("expected nil results for short packet")
	}
}

func TestParsePacketIgnoresBadTag(t *testing.T) {
	pkt := make([]byte, 100)
	pkt[0] = 'X' // wrong tag
	hdr, frm, _, err := parsePacket(pkt)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hdr != nil || frm != nil {
		t.Error("expected nil results for bad tag")
	}
}

func TestNextStreamID(t *testing.T) {
	a := nextStreamID()
	b := nextStreamID()
	if b != a+1 {
		t.Errorf("stream IDs not sequential: %d, %d", a, b)
	}
}
