package radio

import (
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"go.bug.st/serial"
)

// MMDVMRadio implements RadioInterface over the MMDVM serial protocol.
// This is used for radios like the Kenwood TH-D75 that act as MMDVM modems,
// communicating over USB serial.
//
// Note: Bluetooth SPP does not work on macOS — the DriverKit-based BT serial
// driver does not establish the RFCOMM channel when the port is opened.
// Use USB-C to connect to the TH-D75.
type MMDVMRadio struct {
	mu           sync.Mutex
	port         serial.Port
	cfg          Config
	open         bool
	protoVersion int // MMDVM protocol version (1 or 2), from GET_VERSION

	hdrCh  chan dstar.DVHeader
	frmCh  chan dstar.DVFrame
	stopCh chan struct{}
}

// NewMMDVMRadio returns a new, unopened MMDVMRadio.
func NewMMDVMRadio() *MMDVMRadio {
	return &MMDVMRadio{
		hdrCh: make(chan dstar.DVHeader, 8),
		frmCh: make(chan dstar.DVFrame, 32),
	}
}

// mmdvmPortReader wraps a serial.Port to handle timeout detection.
// go.bug.st/serial returns (0, nil) on timeout; this converts that to errMmdvmTimeout.
type mmdvmPortReader struct {
	port serial.Port
}

func (r *mmdvmPortReader) Read(p []byte) (int, error) {
	n, err := r.port.Read(p)
	if n == 0 && err == nil {
		return 0, errMmdvmTimeout
	}
	return n, err
}

// errMmdvmTimeout is returned when a read times out with no data.
var errMmdvmTimeout = fmt.Errorf("mmdvm: read timeout")

func (m *MMDVMRadio) Open(cfg Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.open {
		return fmt.Errorf("mmdvm: already open")
	}

	// MMDVM protocol always uses 115200 baud (confirmed by BlueDV pcap).
	baud := 115200

	stopBits := serial.OneStopBit
	if cfg.StopBits == 2 {
		stopBits = serial.TwoStopBits
	}
	parity := serial.NoParity
	switch cfg.Parity {
	case "E", "e":
		parity = serial.EvenParity
	case "O", "o":
		parity = serial.OddParity
	}
	dataBits := cfg.DataBits
	if dataBits == 0 {
		dataBits = 8
	}
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: dataBits,
		StopBits: stopBits,
		Parity:   parity,
	}
	p, err := serial.Open(cfg.Port, mode)
	if err != nil {
		return fmt.Errorf("mmdvm: open %s: %w", cfg.Port, err)
	}

	// Set DTR high — required for TH-D75 (confirmed by BlueDV docs).
	if err := p.SetDTR(true); err != nil {
		log.Printf("mmdvm: warning: SetDTR failed: %v", err)
	}
	// Set RTS high (DroidStar does this).
	if err := p.SetRTS(true); err != nil {
		log.Printf("mmdvm: warning: SetRTS failed: %v", err)
	}

	m.port = p
	m.cfg = cfg
	m.open = true
	m.stopCh = make(chan struct{})
	log.Printf("mmdvm: opened %s at %d baud", cfg.Port, baud)

	// Give the modem time to initialize after port opens (per MMDVMHost).
	log.Printf("mmdvm: waiting 2s for modem to initialize…")
	time.Sleep(2 * time.Second)

	// Drain any bytes the modem may have sent during the init delay.
	m.drainPort()

	// Attempt MMDVM handshake: GET_VERSION → SET_FREQ → SET_CONFIG.
	// If the handshake fails, log the error but continue anyway —
	// the radio may not require a full handshake and might start
	// responding once we begin sending status polls.
	if err := m.handshake(); err != nil {
		log.Printf("mmdvm: handshake failed: %v — continuing with status polling", err)
	}

	go m.readLoop()
	go m.statusLoop()

	// After starting the read/status loops, log what we're doing so the user
	// knows the radio is being polled even if the handshake failed.
	log.Printf("mmdvm: readLoop and statusLoop started — listening for radio data")
	return nil
}

// reader returns a timeout-aware io.Reader wrapping the serial port.
func (m *MMDVMRadio) reader() io.Reader {
	return &mmdvmPortReader{port: m.port}
}

// drainPort reads and logs any bytes sitting in the serial buffer.
func (m *MMDVMRadio) drainPort() {
	m.port.SetReadTimeout(500 * time.Millisecond) //nolint:errcheck
	buf := make([]byte, 256)
	total := 0
	for {
		n, err := m.port.Read(buf)
		if n > 0 {
			log.Printf("mmdvm: drain: read %d bytes: % 02X", n, buf[:n])
			total += n
		}
		if n == 0 || err != nil {
			break
		}
	}
	if total == 0 {
		log.Printf("mmdvm: drain: no data from radio during init period")
	} else {
		log.Printf("mmdvm: drain: %d total bytes drained", total)
	}
}

// handshake performs the MMDVM initialization:
// GET_VERSION → SET_FREQ → SET_CONFIG (per MMDVMHost and DroidStar ordering).
// Retries GET_VERSION up to 6 times with 1.5s gaps (matching MMDVMHost behavior).
func (m *MMDVMRadio) handshake() error {
	log.Printf("mmdvm: starting handshake")
	m.port.SetReadTimeout(2 * time.Second) //nolint:errcheck

	const maxRetries = 6
	for attempt := range maxRetries {
		log.Printf("mmdvm: sending GET_VERSION (attempt %d/%d)", attempt+1, maxRetries)
		if err := mmdvmWriteGetVersion(m.port); err != nil {
			return fmt.Errorf("write GET_VERSION: %w", err)
		}

		// Poll for response: up to 5 reads with 2s timeout each.
		r := m.reader()
		for i := range 5 {
			log.Printf("mmdvm: reading frame (poll %d/5)…", i+1)
			frm, err := mmdvmReadFrame(r)
			if err != nil {
				if err != errMmdvmTimeout {
					log.Printf("mmdvm: read error: %v", err)
				} else {
					log.Printf("mmdvm: read timeout")
				}
				continue
			}
			log.Printf("mmdvm: got frame cmd=0x%02X (%d payload bytes)", frm.cmd, len(frm.payload))
			if frm.cmd == mmdvmGetVersion {
				ver, desc := mmdvmParseVersionResponse(frm.payload)
				m.protoVersion = ver
				log.Printf("mmdvm: modem version %d — %s", ver, desc)
				return m.handshakeConfigSequence()
			}
			log.Printf("mmdvm: handshake: skipping frame cmd=0x%02X", frm.cmd)
		}

		if attempt < maxRetries-1 {
			log.Printf("mmdvm: no GET_VERSION response, retrying in 1.5s…")
			time.Sleep(1500 * time.Millisecond)
		}
	}

	return fmt.Errorf("timeout waiting for GET_VERSION response after %d attempts — check: (1) radio in Reflector TERM Mode (Menu 650), (2) Menu 985 set to Bluetooth", maxRetries)
}

// handshakeConfigSequence sends SET_MODE(idle) → SET_FREQ → SET_CONFIG → SET_MODE(D-STAR).
// This sequence matches the BlueDV pcap captured against a TH-D75.
func (m *MMDVMRadio) handshakeConfigSequence() error {
	r := m.reader()

	// SET_MODE(idle) — BlueDV sends this before SET_FREQ.
	log.Printf("mmdvm: sending SET_MODE(idle)")
	if err := mmdvmWriteSetMode(m.port, mmdvmModeIdle); err != nil {
		return fmt.Errorf("write SET_MODE(idle): %w", err)
	}
	if err := m.waitForACK(r, "SET_MODE(idle)"); err != nil {
		log.Printf("mmdvm: %v — continuing", err)
	}

	log.Printf("mmdvm: sending SET_FREQ")
	if err := mmdvmWriteSetFreq(m.port); err != nil {
		return fmt.Errorf("write SET_FREQ: %w", err)
	}
	if err := m.waitForACK(r, "SET_FREQ"); err != nil {
		log.Printf("mmdvm: %v — continuing", err)
	}

	log.Printf("mmdvm: sending SET_CONFIG (D-STAR, protocol v%d)", m.protoVersion)
	if err := mmdvmWriteSetConfig(m.port, m.protoVersion); err != nil {
		return fmt.Errorf("write SET_CONFIG: %w", err)
	}
	if err := m.waitForACK(r, "SET_CONFIG"); err != nil {
		log.Printf("mmdvm: %v — continuing anyway", err)
	}

	// SET_MODE(D-STAR) — activates D-STAR mode on the modem.
	// BlueDV pcap confirms this is sent after SET_CONFIG and is required
	// for the TH-D75 to start processing D-STAR data.
	log.Printf("mmdvm: sending SET_MODE(D-STAR)")
	if err := mmdvmWriteSetMode(m.port, mmdvmModeDStar); err != nil {
		return fmt.Errorf("write SET_MODE(D-STAR): %w", err)
	}
	if err := m.waitForACK(r, "SET_MODE(D-STAR)"); err != nil {
		log.Printf("mmdvm: %v — continuing anyway", err)
	}

	// Clear read timeout for normal operation.
	m.port.SetReadTimeout(0) //nolint:errcheck
	return nil
}

// waitForACK reads frames until an ACK or NAK is received, with a 5s timeout.
func (m *MMDVMRadio) waitForACK(r io.Reader, cmdName string) error {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		frm, err := mmdvmReadFrame(r)
		if err != nil {
			continue
		}
		if frm.cmd == mmdvmACK {
			log.Printf("mmdvm: %s ACK received", cmdName)
			return nil
		}
		if frm.cmd == mmdvmNAK {
			reason := byte(0)
			if len(frm.payload) >= 2 {
				reason = frm.payload[1]
			}
			return fmt.Errorf("%s NAK (reason=%d)", cmdName, reason)
		}
		log.Printf("mmdvm: handshake: skipping frame cmd=0x%02X while waiting for %s ACK", frm.cmd, cmdName)
	}
	return fmt.Errorf("timeout waiting for %s ACK", cmdName)
}

func (m *MMDVMRadio) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.open {
		return nil
	}
	close(m.stopCh)
	err := m.port.Close()
	m.open = false
	return err
}

func (m *MMDVMRadio) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}

func (m *MMDVMRadio) SendHeader(hdr dstar.DVHeader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.open {
		return fmt.Errorf("mmdvm: port not open")
	}
	return mmdvmWriteDStarHeader(m.port, hdr)
}

func (m *MMDVMRadio) SendFrame(f dstar.DVFrame) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.open {
		return fmt.Errorf("mmdvm: port not open")
	}
	if err := mmdvmWriteDStarData(m.port, f); err != nil {
		return err
	}
	if f.End {
		if err := mmdvmWriteDStarEOT(m.port); err != nil {
			log.Printf("mmdvm: EOT write error: %v", err)
		}
	}
	return nil
}

func (m *MMDVMRadio) RxHeaders() <-chan dstar.DVHeader { return m.hdrCh }
func (m *MMDVMRadio) RxFrames() <-chan dstar.DVFrame   { return m.frmCh }

// PTT is a no-op for MMDVM — the modem handles TX/RX state transitions
// based on data flow (sending header starts TX, EOT ends it).
func (m *MMDVMRadio) PTT(bool) error {
	return nil
}

// readLoop runs in a background goroutine, reading MMDVM frames and
// dispatching D-STAR headers/voice to the RX channels.
func (m *MMDVMRadio) readLoop() {
	log.Printf("mmdvm: readLoop started")
	r := m.reader()
	firstStatus := false

	for {
		select {
		case <-m.stopCh:
			return
		default:
		}

		frm, err := mmdvmReadFrame(r)
		if err != nil {
			select {
			case <-m.stopCh:
				return
			default:
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				log.Printf("mmdvm: port closed")
				return
			}
			// Don't spam timeout errors during normal operation.
			if err != errMmdvmTimeout {
				log.Printf("mmdvm: read error: %v", err)
			}
			continue
		}

		switch frm.cmd {
		case mmdvmGetStatus:
			status := mmdvmParseStatusResponse(frm.payload)
			if !firstStatus {
				log.Printf("mmdvm: first status — modes=0x%02X state=%d dstarSpace=%d",
					status.enabledModes, status.modemState, status.dstarSpace)
				firstStatus = true
			}

		case mmdvmGetVersion:
			ver, desc := mmdvmParseVersionResponse(frm.payload)
			log.Printf("mmdvm: version response — v%d %s", ver, desc)

		case mmdvmDStarHeader:
			if len(frm.payload) < dstar.HeaderBytes {
				log.Printf("mmdvm: D-STAR header too short (%d bytes)", len(frm.payload))
				continue
			}
			var raw [dstar.HeaderBytes]byte
			copy(raw[:], frm.payload[:dstar.HeaderBytes])
			h, err := dstar.DecodeHeader(raw)
			if err != nil {
				log.Printf("mmdvm: header decode error: %v", err)
				continue
			}
			log.Printf("mmdvm: RX header: %s → %s", h.MyCall, h.YourCall)
			m.hdrCh <- h

		case mmdvmDStarData:
			if len(frm.payload) < dstar.FrameBytes {
				log.Printf("mmdvm: D-STAR voice frame too short (%d bytes)", len(frm.payload))
				continue
			}
			f := dstar.DVFrame{}
			copy(f.AMBE[:], frm.payload[:9])
			copy(f.SlowData[:], frm.payload[9:12])
			m.frmCh <- f

		case mmdvmDStarLost:
			log.Printf("mmdvm: D-STAR signal lost")
			m.frmCh <- dstar.DVFrame{End: true}

		case mmdvmDStarEOT:
			log.Printf("mmdvm: D-STAR end of transmission")
			m.frmCh <- dstar.DVFrame{End: true}

		case mmdvmACK:
			if len(frm.payload) >= 1 {
				log.Printf("mmdvm: ACK for cmd 0x%02X", frm.payload[0])
			}

		case mmdvmNAK:
			reason := byte(0)
			if len(frm.payload) >= 2 {
				reason = frm.payload[1]
			}
			log.Printf("mmdvm: NAK cmd=0x%02X reason=%d", frm.payload[0], reason)

		default:
			log.Printf("mmdvm: unhandled frame cmd=0x%02X (%d payload bytes)", frm.cmd, len(frm.payload))
		}
	}
}

// statusLoop sends GET_STATUS every 250ms as a keepalive.
func (m *MMDVMRadio) statusLoop() {
	log.Printf("mmdvm: statusLoop started")
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.open {
				if err := mmdvmWriteGetStatus(m.port); err != nil {
					log.Printf("mmdvm: status poll error: %v", err)
				}
			}
			m.mu.Unlock()
		}
	}
}
