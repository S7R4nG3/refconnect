package dcs

import (
	"testing"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

func TestBuildConnectPacket(t *testing.T) {
	pkt := buildConnectPacket("KR4GCQ D", 'D', 'C')
	if len(pkt) != connectPacketLen {
		t.Fatalf("connect packet length = %d, want %d", len(pkt), connectPacketLen)
	}
	if string(pkt[0:8]) != "KR4GCQ D" {
		t.Errorf("callsign = %q, want %q", string(pkt[0:8]), "KR4GCQ D")
	}
	if pkt[8] != 'D' {
		t.Errorf("local module = %c, want D", pkt[8])
	}
	if pkt[9] != 'C' {
		t.Errorf("target module = %c, want C", pkt[9])
	}
	// Rest should be zeros.
	for i := 10; i < connectPacketLen; i++ {
		if pkt[i] != 0 {
			t.Errorf("byte[%d] = 0x%02X, want 0x00", i, pkt[i])
			break
		}
	}
}

func TestBuildDisconnectPacket(t *testing.T) {
	pkt := buildDisconnectPacket("KR4GCQ D", 'C')
	if len(pkt) != disconnectPacketLen {
		t.Fatalf("disconnect packet length = %d, want %d", len(pkt), disconnectPacketLen)
	}
	if string(pkt[0:8]) != "KR4GCQ D" {
		t.Errorf("callsign = %q, want %q", string(pkt[0:8]), "KR4GCQ D")
	}
	if pkt[8] != 'C' {
		t.Errorf("module = %c, want C", pkt[8])
	}
	if pkt[9] != ' ' {
		t.Errorf("byte[9] = 0x%02X, want 0x20 (space)", pkt[9])
	}
}

func TestBuildKeepalive(t *testing.T) {
	pkt := buildKeepalive("KR4GCQ D", 'D', "DCS001  ", 'C')
	if len(pkt) != keepalivePacketLen {
		t.Fatalf("keepalive length = %d, want %d", len(pkt), keepalivePacketLen)
	}
	if string(pkt[0:7]) != "KR4GCQ " {
		t.Errorf("client callsign = %q, want %q", string(pkt[0:7]), "KR4GCQ ")
	}
	if pkt[7] != 'D' {
		t.Errorf("client module = %c, want D", pkt[7])
	}
	if pkt[8] != 0x00 {
		t.Errorf("separator = 0x%02X, want 0x00", pkt[8])
	}
	if string(pkt[9:16]) != "DCS001 " {
		t.Errorf("reflector callsign = %q, want %q", string(pkt[9:16]), "DCS001 ")
	}
	if pkt[16] != 'C' {
		t.Errorf("reflector module = %c, want C", pkt[16])
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

	pkt, err := encodeVoicePacket(0x1234, 5, false, hdr, frm)
	if err != nil {
		t.Fatalf("encodeVoicePacket: %v", err)
	}
	if len(pkt) != voicePacketLen {
		t.Fatalf("voice packet length = %d, want %d", len(pkt), voicePacketLen)
	}
	// Check tag.
	if string(pkt[0:4]) != "0001" {
		t.Errorf("tag = %q, want %q", string(pkt[0:4]), "0001")
	}

	// Parse it back.
	gotHdr, gotFrm, gotSID, err := parsePacket(pkt)
	if err != nil {
		t.Fatalf("parsePacket: %v", err)
	}
	if gotHdr == nil || gotFrm == nil {
		t.Fatal("parsePacket returned nil header or frame")
	}
	if gotSID != 0x1234 {
		t.Errorf("stream ID = 0x%04X, want 0x1234", gotSID)
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

	pkt, err := encodeVoicePacket(0xABCD, 20, true, hdr, frm)
	if err != nil {
		t.Fatalf("encodeVoicePacket: %v", err)
	}

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
	// Keepalive-length packets should be silently ignored.
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
