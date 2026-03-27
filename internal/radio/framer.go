package radio

import (
	"encoding/binary"
	"fmt"
	"io"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// frameType distinguishes the two wire frame types used over serial.
const (
	frameTypeHeader = 0x10
	frameTypeVoice  = 0x20
)

// Wire framing uses a simple length-prefixed scheme:
//   [0]   frame type (0x10 = header, 0x20 = voice)
//   [1:3] payload length (uint16 big-endian)
//   [3:]  payload bytes
//
// Header payload: 41 bytes (raw D-STAR header including CRC)
// Voice payload:  10 bytes (1 byte seq | 0x40 if last) + 9 bytes AMBE + 3 bytes slow-data)
//
// NOTE: This is a minimal framing convention.  Adapt to your radio's actual
// protocol if it differs (e.g. KISS, DV3000 serial, or proprietary headers).

const (
	wirHeaderPayloadLen = dstar.HeaderBytes // 41
	wireVoicePayloadLen = 1 + dstar.FrameBytes // seq byte + 12 bytes
)

// writeHeader encodes a DVHeader and writes it to w.
func writeHeader(w io.Writer, hdr dstar.DVHeader) error {
	encoded, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return err
	}
	buf := make([]byte, 3+wirHeaderPayloadLen)
	buf[0] = frameTypeHeader
	binary.BigEndian.PutUint16(buf[1:3], uint16(wirHeaderPayloadLen))
	copy(buf[3:], encoded[:])
	_, err = w.Write(buf)
	return err
}

// writeFrame encodes a DVFrame and writes it to w.
func writeFrame(w io.Writer, f dstar.DVFrame) error {
	buf := make([]byte, 3+wireVoicePayloadLen)
	buf[0] = frameTypeVoice
	binary.BigEndian.PutUint16(buf[1:3], uint16(wireVoicePayloadLen))
	seq := f.Seq
	if f.End {
		seq |= 0x40
	}
	buf[3] = seq
	copy(buf[4:13], f.AMBE[:])
	copy(buf[13:16], f.SlowData[:])
	_, err := w.Write(buf)
	return err
}

// readNext reads one framed message from r and dispatches it to the appropriate channel.
// Returns io.EOF when the underlying reader is closed.
func readNext(r io.Reader, hdrCh chan<- dstar.DVHeader, frmCh chan<- dstar.DVFrame) error {
	hdr := make([]byte, 3)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return err
	}
	ftype := hdr[0]
	plen := int(binary.BigEndian.Uint16(hdr[1:3]))

	payload := make([]byte, plen)
	if _, err := io.ReadFull(r, payload); err != nil {
		return err
	}

	switch ftype {
	case frameTypeHeader:
		if plen != wirHeaderPayloadLen {
			return fmt.Errorf("radio: unexpected header payload length %d", plen)
		}
		var raw [dstar.HeaderBytes]byte
		copy(raw[:], payload)
		h, err := dstar.DecodeHeader(raw)
		if err != nil {
			return err
		}
		hdrCh <- h

	case frameTypeVoice:
		if plen != wireVoicePayloadLen {
			return fmt.Errorf("radio: unexpected voice payload length %d", plen)
		}
		seq := payload[0]
		end := seq&0x40 != 0
		seq &^= 0x40
		var f dstar.DVFrame
		f.Seq = seq
		f.End = end
		copy(f.AMBE[:], payload[1:10])
		copy(f.SlowData[:], payload[10:13])
		frmCh <- f

	default:
		return fmt.Errorf("radio: unknown frame type 0x%02X", ftype)
	}
	return nil
}
