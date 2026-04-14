package radio

import (
	"fmt"
	"io"
	"log"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// MMDVM serial protocol framing.
//
// Every MMDVM frame is: [0xE0][LENGTH][COMMAND][PAYLOAD...]
//   0xE0   — frame start marker (always)
//   LENGTH — total frame length (including start byte, length, command, payload)
//   COMMAND — command/type byte
//   PAYLOAD — variable-length data (may be empty)
//
// No terminator byte. Length includes all bytes in the frame.
//
// Reference: g4klx/MMDVM firmware + g4klx/MMDVMHost.
const (
	mmdvmFrameStart = byte(0xE0)

	mmdvmGetVersion  = byte(0x00) // host → modem: request version
	mmdvmGetStatus   = byte(0x01) // host → modem: request status
	mmdvmSetConfig   = byte(0x02) // host → modem: configure modem
	mmdvmSetMode = byte(0x03) // host → modem: set operating mode
	mmdvmSetFreq = byte(0x04) // host → modem: set frequency

	mmdvmDStarHeader = byte(0x10) // bidirectional: D-STAR header (41 bytes)
	mmdvmDStarData   = byte(0x11) // bidirectional: D-STAR voice frame (12 bytes)
	mmdvmDStarLost   = byte(0x12) // modem → host: signal lost
	mmdvmDStarEOT    = byte(0x13) // bidirectional: end of transmission

	mmdvmACK = byte(0x70) // modem → host: command accepted
	mmdvmNAK = byte(0x7F) // modem → host: command rejected

	// Mode flags for SET_CONFIG byte 4.
	mmdvmModeDStar = byte(0x01)

	// Mode values for SET_MODE.
	mmdvmModeIdle = byte(0x00)
)

// mmdvmFrame holds the result of parsing one MMDVM frame.
type mmdvmFrame struct {
	cmd     byte
	payload []byte // bytes after the command byte
}

// D-STAR end-of-transmission pattern: 12 bytes sent with EOT frames.
var dstarEndPattern = [12]byte{
	0x55, 0x55, 0x55, 0x55, 0x55, 0x55,
	0x55, 0x55, 0x55, 0x55, 0xC8, 0x7A,
}

// mmdvmWriteFrame writes a generic MMDVM frame: [0xE0][len][cmd][payload].
func mmdvmWriteFrame(w io.Writer, cmd byte, payload []byte) error {
	length := 3 + len(payload) // start + length + cmd + payload
	if length > 255 {
		return fmt.Errorf("mmdvm: frame too large (%d bytes)", length)
	}
	buf := make([]byte, length)
	buf[0] = mmdvmFrameStart
	buf[1] = byte(length)
	buf[2] = cmd
	copy(buf[3:], payload)
	n, err := w.Write(buf)
	log.Printf("mmdvm: wrote %d bytes: % 02X", n, buf)
	return err
}

// mmdvmReadByte reads exactly one byte from r.
// Returns any error from the underlying reader (including deadline exceeded).
func mmdvmReadByte(r io.Reader) (byte, error) {
	var b [1]byte
	_, err := io.ReadFull(r, b[:])
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// mmdvmReadN reads exactly n bytes from r.
func mmdvmReadN(r io.Reader, n int) ([]byte, error) {
	buf := make([]byte, n)
	_, err := io.ReadFull(r, buf)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// mmdvmReadFrame reads one MMDVM frame from r.
// It scans for the 0xE0 start byte, discarding any garbage bytes.
// Returns errMmdvmTimeout if the serial port times out with no data.
func mmdvmReadFrame(r io.Reader) (mmdvmFrame, error) {
	// Scan for start byte.
	for {
		b, err := mmdvmReadByte(r)
		if err != nil {
			return mmdvmFrame{}, err
		}
		if b == mmdvmFrameStart {
			break
		}
		// Discard non-start bytes (noise, partial frames).
	}

	// Read length byte.
	length, err := mmdvmReadByte(r)
	if err != nil {
		return mmdvmFrame{}, err
	}
	if length < 3 {
		return mmdvmFrame{}, fmt.Errorf("mmdvm: frame length %d too short", length)
	}

	// Read remaining bytes: [cmd][payload...]
	remaining, err := mmdvmReadN(r, int(length)-2)
	if err != nil {
		return mmdvmFrame{}, err
	}

	return mmdvmFrame{
		cmd:     remaining[0],
		payload: remaining[1:],
	}, nil
}

// mmdvmWriteGetVersion sends GET_VERSION: [E0 03 00].
func mmdvmWriteGetVersion(w io.Writer) error {
	return mmdvmWriteFrame(w, mmdvmGetVersion, nil)
}

// mmdvmWriteGetStatus sends GET_STATUS: [E0 03 01].
func mmdvmWriteGetStatus(w io.Writer) error {
	return mmdvmWriteFrame(w, mmdvmGetStatus, nil)
}

// mmdvmWriteSetMode sends SET_MODE: [E0 04 03 <mode>].
// mode=0x00 is idle, mode=0x01 is D-STAR.
// BlueDV pcap confirms SET_MODE is sent before SET_FREQ, after SET_CONFIG,
// and to deactivate D-STAR after EOT.
func mmdvmWriteSetMode(w io.Writer, mode byte) error {
	return mmdvmWriteFrame(w, mmdvmSetMode, []byte{mode})
}

// mmdvmWriteSetFreq sends SET_FREQ with RX and TX frequency.
// Frame: [E0][0C][04][00][rxFreq LE 4 bytes][txFreq LE 4 bytes] = 12 bytes total.
// BlueDV pcap confirms only 9 payload bytes (no rfLevel or POCSAG fields).
// The firmware ACKs without processing the values.
func mmdvmWriteSetFreq(w io.Writer) error {
	freq := uint32(434300000) // 434.3 MHz (matches BlueDV pcap)
	payload := make([]byte, 9)
	// payload[0] = 0x00 (padding)
	payload[1] = byte(freq)
	payload[2] = byte(freq >> 8)
	payload[3] = byte(freq >> 16)
	payload[4] = byte(freq >> 24)
	payload[5] = byte(freq)
	payload[6] = byte(freq >> 8)
	payload[7] = byte(freq >> 16)
	payload[8] = byte(freq >> 24)
	return mmdvmWriteFrame(w, mmdvmSetFreq, payload)
}

// mmdvmWriteSetConfig sends SET_CONFIG to enable D-STAR mode.
// protoVersion selects the config layout: v1 or v2.
//
// The TH-D75 reports protocol v1 and the BlueDV pcap confirms a v1 SET_CONFIG
// with 18 payload bytes (21 total), flags=0x82 (simplex + txInvert).
func mmdvmWriteSetConfig(w io.Writer, protoVersion int) error {
	if protoVersion == 2 {
		// Protocol v2: 40 bytes total (37 payload).
		// Layout matches MMDVMHost setConfig2 (buffer[3..39]).
		payload := make([]byte, 37)
		payload[0] = 0x82 // flags: simplex(0x80) + txInvert(0x02)
		payload[1] = mmdvmModeDStar
		payload[2] = 0    // modes2 (POCSAG enable — not used)
		payload[3] = 0x0A // txDelay (10 × 10ms = 100ms)
		payload[4] = mmdvmModeIdle
		payload[5] = 128 // txDCOffset + 128 (= 0 offset)
		payload[6] = 128 // rxDCOffset + 128 (= 0 offset)
		payload[7] = 128 // rxLevel (~50%)
		payload[8] = 128 // cwIdTXLevel
		payload[9] = 128 // dstarTXLevel
		payload[10] = 0  // dmrTXLevel (unused)
		payload[11] = 0  // ysfTXLevel (unused)
		payload[12] = 0  // p25TXLevel (unused)
		payload[13] = 0  // nxdnTXLevel (unused)
		// payload[14..36] = 0 (pocsagTXLevel, fmTXLevel, hang times, dmrColorCode, etc.)
		return mmdvmWriteFrame(w, mmdvmSetConfig, payload)
	}

	// Protocol v1: 21 bytes total (18 payload).
	// Matches BlueDV pcap: E0 15 02 82 01 0A 00 80 80 01 00 80 7E 7E 7E 7E 80 80 80 04 80
	payload := make([]byte, 18)
	payload[0] = 0x82 // flags: simplex(0x80) + txInvert(0x02)
	payload[1] = mmdvmModeDStar
	payload[2] = 0x0A // txDelay (10 × 10ms = 100ms)
	payload[3] = mmdvmModeIdle
	payload[4] = 128  // rxLevel (50%)
	payload[5] = 128  // cwIdTXLevel
	payload[6] = 0x01 // dmrColorCode
	payload[7] = 0    // dmrDelay
	payload[8] = 128  // oscOffset (128 = 0 ppm)
	payload[9] = 0x7E // dstarTXLevel (126)
	payload[10] = 0x7E // dmrTXLevel
	payload[11] = 0x7E // ysfTXLevel
	payload[12] = 0x7E // p25TXLevel
	payload[13] = 128  // txDCOffset + 128
	payload[14] = 128  // rxDCOffset + 128
	payload[15] = 128  // nxdnTXLevel
	payload[16] = 0x04 // ysfTXHang
	payload[17] = 128  // p25TXHang
	return mmdvmWriteFrame(w, mmdvmSetConfig, payload)
}

// mmdvmWriteDStarHeader sends a D-STAR header to the modem.
// Frame: [E0][2C][10][41-byte D-STAR header with CRC] = 44 bytes total.
func mmdvmWriteDStarHeader(w io.Writer, hdr dstar.DVHeader) error {
	encoded, err := dstar.EncodeHeader(hdr)
	if err != nil {
		return fmt.Errorf("mmdvm: encode header: %w", err)
	}
	return mmdvmWriteFrame(w, mmdvmDStarHeader, encoded[:])
}

// mmdvmWriteDStarData sends a D-STAR voice frame to the modem.
// Frame: [E0][0F][11][9 AMBE + 3 SlowData] = 15 bytes total.
func mmdvmWriteDStarData(w io.Writer, f dstar.DVFrame) error {
	var payload [12]byte
	copy(payload[:9], f.AMBE[:])
	copy(payload[9:12], f.SlowData[:])
	return mmdvmWriteFrame(w, mmdvmDStarData, payload[:])
}

// mmdvmWriteDStarEOT sends a D-STAR end-of-transmission to the modem.
// Frame: [E0][0F][13][end pattern 12 bytes] = 15 bytes total.
func mmdvmWriteDStarEOT(w io.Writer) error {
	return mmdvmWriteFrame(w, mmdvmDStarEOT, dstarEndPattern[:])
}

// mmdvmParseVersionResponse extracts the protocol version and firmware
// description from a GET_VERSION response payload.
func mmdvmParseVersionResponse(payload []byte) (version int, description string) {
	if len(payload) < 1 {
		return 1, "unknown"
	}
	version = int(payload[0])
	if version < 1 || version > 2 {
		log.Printf("mmdvm: unexpected protocol version %d, assuming v1", version)
		version = 1
	}
	if len(payload) > 1 {
		description = string(payload[1:])
	} else {
		description = "unknown"
	}
	return version, description
}

// mmdvmParseStatusResponse extracts key fields from a GET_STATUS response.
type mmdvmStatus struct {
	enabledModes byte
	modemState   byte
	dstarSpace   byte // number of D-STAR frames the modem can buffer
}

func mmdvmParseStatusResponse(payload []byte) mmdvmStatus {
	var s mmdvmStatus
	if len(payload) >= 1 {
		s.enabledModes = payload[0]
	}
	if len(payload) >= 2 {
		s.modemState = payload[1]
	}
	if len(payload) >= 4 {
		s.dstarSpace = payload[3]
	}
	return s
}
