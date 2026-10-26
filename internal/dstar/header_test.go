package dstar

import (
	"errors"
	"testing"
)

func TestDecodeNetworkHeader(t *testing.T) {
	raw, err := EncodeHeader(DVHeader{
		RPT1:     "XLX299 C",
		RPT2:     "XLX299 G",
		YourCall: "CQCQCQ  ",
		MyCall:   "ZL1ABC  ",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Valid CRC is accepted.
	if _, err := DecodeNetworkHeader(raw); err != nil {
		t.Errorf("valid CRC rejected: %v", err)
	}

	// Placeholder CRCs sent by xlxd are accepted.
	for _, crc := range [][2]byte{{0x00, 0x00}, {0xFF, 0xFF}} {
		r := raw
		r[39], r[40] = crc[0], crc[1]
		h, err := DecodeNetworkHeader(r)
		if err != nil {
			t.Errorf("placeholder CRC %X rejected: %v", crc, err)
		}
		if h.MyCall != "ZL1ABC  " {
			t.Errorf("MyCall = %q", h.MyCall)
		}
		if _, err := DecodeHeader(r); err == nil {
			t.Errorf("DecodeHeader accepted placeholder CRC %X", crc)
		}
	}

	// Any other wrong CRC still decodes, reported as a *CRCError.
	r := raw
	r[39], r[40] = 0x0B, 0x00
	h, err := DecodeNetworkHeader(r)
	var crcErr *CRCError
	if !errors.As(err, &crcErr) {
		t.Fatalf("wrong CRC: err = %v, want *CRCError", err)
	}
	if crcErr.Got != 0x000B {
		t.Errorf("CRCError.Got = %04X, want 000B", crcErr.Got)
	}
	if h.MyCall != "ZL1ABC  " || h.RPT1 != "XLX299 C" {
		t.Errorf("header not decoded with wrong CRC: %+v", h)
	}
	if _, err := DecodeHeader(r); !errors.As(err, &crcErr) {
		t.Errorf("DecodeHeader with wrong CRC: err = %v, want *CRCError", err)
	}
}
