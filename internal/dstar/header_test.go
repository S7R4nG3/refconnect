package dstar

import "testing"

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

	// Any other wrong CRC is still rejected.
	r := raw
	r[39] ^= 0x01
	if _, err := DecodeNetworkHeader(r); err == nil {
		t.Error("corrupt CRC accepted")
	}
}
