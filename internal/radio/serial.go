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

// SerialRadio implements RadioInterface over a DV Gateway Terminal serial port.
type SerialRadio struct {
	mu   sync.Mutex
	port serial.Port
	cfg  Config
	open bool

	txVoiceSeq uint8 // absolute TX voice frame counter, reset on each new header

	hdrCh  chan dstar.DVHeader
	frmCh  chan dstar.DVFrame
	stopCh chan struct{}
	wg     sync.WaitGroup // tracks readLoop + pollLoop
}

// NewSerialRadio returns a new, unopened SerialRadio.
func NewSerialRadio() *SerialRadio {
	return &SerialRadio{
		hdrCh: make(chan dstar.DVHeader, 8),
		frmCh: make(chan dstar.DVFrame, 32),
	}
}

func (r *SerialRadio) Open(cfg Config) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.open {
		return fmt.Errorf("radio: already open")
	}

	// DV Gateway Terminal protocol always uses 38400 baud (confirmed by RS-MS3W pcap).
	baud := 38400

	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	}
	p, err := serial.Open(cfg.Port, mode)
	if err != nil {
		return fmt.Errorf("radio: open %s: %w", cfg.Port, err)
	}
	r.port = p
	r.cfg = cfg
	r.open = true
	r.stopCh = make(chan struct{})
	log.Printf("radio: opened %s at %d baud", cfg.Port, baud)

	// Send FF FF FF init/flush sequence. This clears any partial frame state
	// in the radio's DV Gateway Terminal parser and primes the interface.
	// Observed in RS-MS3W pcap — the radio echoes it back, then responds
	// normally to polls. Without this, macOS gets only single FF bytes back.
	if err := writeInit(p); err != nil {
		log.Printf("radio: init flush write error: %v", err)
	} else {
		log.Printf("radio: init flush sent (FF FF FF)")
	}
	// Brief pause to let the radio process the flush before polling begins.
	time.Sleep(100 * time.Millisecond)

	r.wg.Add(2)
	go r.readLoop()
	go r.pollLoop()
	return nil
}

func (r *SerialRadio) Close() error {
	r.mu.Lock()
	if !r.open {
		r.mu.Unlock()
		return nil
	}
	close(r.stopCh)
	err := r.port.Close()
	r.open = false
	r.mu.Unlock()
	// Wait for goroutines to exit after releasing the lock (they need it).
	r.wg.Wait()
	return err
}

func (r *SerialRadio) IsOpen() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.open
}

func (r *SerialRadio) SendHeader(hdr dstar.DVHeader) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open {
		return fmt.Errorf("radio: port not open")
	}
	r.txVoiceSeq = 0 // reset voice sequence counter for the new transmission
	return writeHeader(r.port, hdr)
}

func (r *SerialRadio) SendFrame(f dstar.DVFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open {
		return fmt.Errorf("radio: port not open")
	}
	seq := r.txVoiceSeq
	r.txVoiceSeq++
	return writeFrame(r.port, seq, f)
}

func (r *SerialRadio) RxHeaders() <-chan dstar.DVHeader { return r.hdrCh }
func (r *SerialRadio) RxFrames() <-chan dstar.DVFrame   { return r.frmCh }

func (r *SerialRadio) PTT(on bool) error {
	return nil
}

// readLoop runs in a background goroutine, parsing DV Gateway Terminal frames
// and dispatching headers and voice frames to their respective channels.
func (r *SerialRadio) readLoop() {
	defer r.wg.Done()
	log.Printf("radio: readLoop started")
	pollAckLogged := false

	// Warn if we haven't heard anything from the radio after the first few polls.
	// This fires once and helps distinguish "wrong port" from "wrong protocol".
	noResponseTimer := time.AfterFunc(5*time.Second, func() {
		if !pollAckLogged {
			log.Printf("radio: WARNING — no response from radio after 5s of polling")
			log.Printf("radio: check: (1) correct port selected, (2) radio in DV mode,")
			log.Printf("radio: (3) USB connected to the port that showed DV data in device manager")
		}
	})
	defer noResponseTimer.Stop()

	for {
		frm, err := readFrame(r.port)
		if err != nil {
			select {
			case <-r.stopCh:
				return
			default:
			}
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				log.Printf("radio: port closed")
				return
			}
			log.Printf("radio: read error: %v", err)
			continue
		}

		switch frm.ftype {
		case 0xFF:
			// Init flush echo or inter-frame noise — silently skip.
			continue
		case typePollAck:
			if !pollAckLogged {
				noResponseTimer.Stop()
				log.Printf("radio: first poll ACK received — radio is communicating")
				pollAckLogged = true
			}
		case typeTXHeaderAck:
			log.Printf("radio: TX header ACK")
		case typeTXVoiceAck:
			// voice frame ACK — no action needed
		case typeRXHeader:
			if frm.header != nil {
				r.hdrCh <- *frm.header
			}
		case typeRXVoice:
			if frm.voice != nil {
				r.frmCh <- *frm.voice
			}
		}
	}
}

// pollLoop sends a poll keepalive to the radio every second.
// The radio requires regular polls or it stops sending DV data.
func (r *SerialRadio) pollLoop() {
	defer r.wg.Done()
	log.Printf("radio: pollLoop started")
	pollCount := 0
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			r.mu.Lock()
			if r.open {
				if err := writePoll(r.port); err != nil {
					log.Printf("radio: poll error: %v", err)
				} else {
					pollCount++
					if pollCount <= 5 || pollCount%10 == 0 {
						log.Printf("radio: poll sent (#%d)", pollCount)
					}
				}
			}
			r.mu.Unlock()
		}
	}
}
