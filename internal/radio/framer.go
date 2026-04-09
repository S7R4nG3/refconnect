package radio

import (
	"fmt"
	"io"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// DV Gateway Terminal serial protocol.
//
// Framing: [LEN][TYPE][DATA...][0xFF]
//   LEN  — 1 byte; equals (total frame length − 1), i.e. LEN + 1 = total bytes.
//   TYPE — 1 byte; direction-specific (RX from radio vs TX to radio).
//   DATA — LEN − 2 bytes.
//   0xFF — terminator, always the last byte.
//
// Frame types — RX path (radio → host):
//   0x03  Poll ACK            1 data byte (status)
//   0x10  DV header           42 data bytes (41-byte D-STAR header + 1 reserved)
//   0x12  DV voice frame      14 data bytes (seq1, seq2, 9 AMBE, 3 slow-data)
//   0x21  TX header ACK       1 data byte
//   0x23  TX voice ACK        2 data bytes (echoed seq1, seq2)
//
// Frame types — TX path (host → radio):
//   0x02  Poll                no data
//   0x20  DV header           39 data bytes (FLAG1=0x01, FLAG2, FLAG3, callsigns, no CRC)
//   0x22  DV voice frame      14 data bytes (seq1, seq2, 9 AMBE, 3 slow-data)
//
// Voice seq2 byte: lower 6 bits = D-STAR sequence (0–20); bit 6 (0x40) set on last frame.
const (
	typePoll        = byte(0x02) // host → radio keepalive
	typePollAck     = byte(0x03) // radio → host keepalive response
	typeRXHeader    = byte(0x10) // radio → host DV header
	typeRXVoice     = byte(0x12) // radio → host DV voice frame
	typeTXHeader    = byte(0x20) // host → radio DV header
	typeTXHeaderAck = byte(0x21) // radio → host TX header ACK
	typeTXVoice     = byte(0x22) // host → radio DV voice frame
	typeTXVoiceAck  = byte(0x23) // radio → host TX voice ACK

	frameTerminator = byte(0xFF)

	// txHeaderDataLen: 3 flag bytes + 4×8-char callsigns + 4-char suffix = 39.
	// The radio generates its own CRC; do not include it in the TX header.
	txHeaderDataLen = 3 + 8 + 8 + 8 + 8 + 4 // = 39

	// voiceDataLen: seq1(1) + seq2(1) + AMBE(9) + slow-data(3) = 14.
	voiceDataLen = 1 + 1 + 9 + 3 // = 14
)

// icomFrame holds the result of parsing one DV Gateway Terminal frame.
type icomFrame struct {
	ftype  byte
	header *dstar.DVHeader
	voice  *dstar.DVFrame
}

// writeInit writes a 3-byte init/flush sequence (FF FF FF) to w.
// This clears any partial frame state in the radio's DV Gateway Terminal
// parser. The radio echoes back FF FF FF, after which polls work normally.
// Observed in RS-MS3W pcap; without this, macOS gets only single FF bytes.
func writeInit(w io.Writer) error {
	_, err := w.Write([]byte{frameTerminator, frameTerminator, frameTerminator})
	return err
}

// writePoll writes a 3-byte poll keepalive frame to w.
// Frame: [LEN=2][TYPE=0x02][0xFF]
func writePoll(w io.Writer) error {
	_, err := w.Write([]byte{0x02, typePoll, frameTerminator})
	return err
}

// writeHeader encodes hdr and writes a TX DV header frame to w.
// The radio generates its own CRC, so only 39 bytes of callsign/flag data
// are sent (the standard 41-byte header minus the 2-byte CRC).
// FLAG1 is set to 0x01 to signal the TX direction to the radio.
func writeHeader(w io.Writer, hdr dstar.DVHeader) error {
	encoded, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return err
	}
	// Frame layout (42 bytes):
	//   [0]     LEN = 41
	//   [1]     TYPE = 0x20
	//   [2]     FLAG1 = 0x01 (TX direction)
	//   [3]     FLAG2
	//   [4]     FLAG3
	//   [5..40] callsigns: RPT2(8)+RPT1(8)+URCALL(8)+MYCALL(8)+MYCALL2(4) = 36 bytes
	//   [41]    0xFF
	const frameLen = 1 + 1 + txHeaderDataLen + 1 // LEN + TYPE + data + FF = 42
	var buf [frameLen]byte
	buf[0] = byte(frameLen - 1) // LEN = 41
	buf[1] = typeTXHeader
	buf[2] = 0x01        // FLAG1: TX direction
	buf[3] = encoded[1]  // FLAG2
	buf[4] = encoded[2]  // FLAG3
	copy(buf[5:41], encoded[3:39]) // RPT2+RPT1+URCALL+MYCALL+MYCALL2 (36 bytes)
	buf[41] = frameTerminator
	_, err = w.Write(buf[:])
	return err
}

// writeFrame encodes f and writes a TX DV voice frame to w.
// txSeq is the absolute frame counter maintained by the caller; it is sent
// as seq1. seq2 carries the D-STAR sequence number (0–20) from f.Seq, with
// bit 6 (0x40) set when f.End is true to mark the end of transmission.
func writeFrame(w io.Writer, txSeq uint8, f dstar.DVFrame) error {
	// Frame layout (17 bytes):
	//   [0]     LEN = 16
	//   [1]     TYPE = 0x22
	//   [2]     seq1 (absolute TX frame counter)
	//   [3]     seq2 = f.Seq | 0x40 if last frame
	//   [4..12] AMBE (9 bytes)
	//   [13..15] slow-data (3 bytes)
	//   [16]    0xFF
	const frameLen = 1 + 1 + voiceDataLen + 1 // = 17
	var buf [frameLen]byte
	buf[0] = byte(frameLen - 1) // LEN = 16
	buf[1] = typeTXVoice
	buf[2] = txSeq
	seq2 := f.Seq
	if f.End {
		seq2 |= 0x40
	}
	buf[3] = seq2
	copy(buf[4:13], f.AMBE[:])
	copy(buf[13:16], f.SlowData[:])
	buf[16] = frameTerminator
	_, err := w.Write(buf[:])
	return err
}

// readFrame reads one DV Gateway Terminal frame from r.
// Returns io.EOF or io.ErrUnexpectedEOF when the reader is closed.
func readFrame(r io.Reader) (icomFrame, error) {
	// Read the LEN byte.
	var lenBuf [1]byte
	if _, err := io.ReadFull(r, lenBuf[:]); err != nil {
		return icomFrame{}, err
	}
	length := int(lenBuf[0])

	// Skip 0xFF bytes — these are init/flush echoes or inter-frame noise.
	// The radio echoes back FF FF FF after the init sequence; these are not
	// valid frame starts (no real frame has LEN=255).
	if length == 0xFF {
		return icomFrame{ftype: 0xFF}, nil
	}

	if length < 2 {
		return icomFrame{}, fmt.Errorf("radio: frame LEN 0x%02X too short (raw byte received — wrong port or radio not in DV Gateway Terminal mode?)", lenBuf[0])
	}

	// Read the remaining LEN bytes: [TYPE][DATA...][0xFF].
	content := make([]byte, length)
	if _, err := io.ReadFull(r, content); err != nil {
		return icomFrame{}, err
	}

	// Build the full raw frame for error messages: [LEN][TYPE][DATA...][0xFF]
	rawFrame := make([]byte, 1+length)
	rawFrame[0] = lenBuf[0]
	copy(rawFrame[1:], content)

	if content[length-1] != frameTerminator {
		return icomFrame{}, fmt.Errorf("radio: bad frame terminator — raw: % 02X", rawFrame)
	}

	ftype := content[0]
	data := content[1 : length-1] // bytes between TYPE and 0xFF

	switch ftype {
	case typePollAck, typeTXHeaderAck, typeTXVoiceAck:
		return icomFrame{ftype: ftype}, nil

	case typeRXHeader:
		if len(data) < dstar.HeaderBytes {
			return icomFrame{}, fmt.Errorf("radio: RX header too short (%d bytes) — raw: % 02X", len(data), rawFrame)
		}
		var raw [dstar.HeaderBytes]byte
		copy(raw[:], data[:dstar.HeaderBytes])
		h, err := dstar.DecodeHeader(raw)
		if err != nil {
			return icomFrame{}, fmt.Errorf("radio: header decode: %w — raw: % 02X", err, rawFrame)
		}
		return icomFrame{ftype: ftype, header: &h}, nil

	case typeRXVoice:
		// data = [seq1][seq2][9 AMBE][3 slow-data]
		if len(data) < voiceDataLen {
			return icomFrame{}, fmt.Errorf("radio: RX voice frame too short (%d bytes) — raw: % 02X", len(data), rawFrame)
		}
		// seq1 (data[0]) is the absolute frame counter; not needed for routing.
		seq2 := data[1]
		end := seq2&0x40 != 0
		seq2 &^= 0x40
		f := &dstar.DVFrame{
			Seq: seq2,
			End: end,
		}
		copy(f.AMBE[:], data[2:11])
		copy(f.SlowData[:], data[11:14])
		return icomFrame{ftype: ftype, voice: f}, nil

	default:
		return icomFrame{}, fmt.Errorf("radio: unknown frame type 0x%02X — raw: % 02X", ftype, rawFrame)
	}
}
