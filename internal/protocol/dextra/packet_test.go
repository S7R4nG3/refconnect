package dextra

import (
	"errors"
	"testing"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// A header relayed with a wrong CRC is still decoded, with a *dstar.CRCError.
func TestParsePacketAcceptsHeaderWithBadCRC(t *testing.T) {
	pkt, err := encodeHeader(0x1234, dstar.DVHeader{
		RPT1: "VK5ACX B", RPT2: "REF001 C", YourCall: "CQCQCQ  ", MyCall: "VK5ACX  ",
	})
	if err != nil {
		t.Fatal(err)
	}
	pkt[54], pkt[54+1] = 0x0B, 0x00 // stored CRC 0B00, as seen from REF001

	hdr, frm, err := parsePacket(pkt)
	var crcErr *dstar.CRCError
	if !errors.As(err, &crcErr) {
		t.Fatalf("err = %v, want *dstar.CRCError", err)
	}
	if hdr == nil || frm != nil {
		t.Fatalf("hdr = %v, frm = %v; want a header", hdr, frm)
	}
	if hdr.MyCall != "VK5ACX  " || hdr.RPT2 != "REF001 C" {
		t.Errorf("decoded header = %+v", *hdr)
	}

	// The same packet with its CRC intact parses cleanly.
	good, _ := encodeHeader(0x1234, *hdr)
	if _, _, err := parsePacket(good); err != nil {
		t.Errorf("valid header: %v", err)
	}
}
