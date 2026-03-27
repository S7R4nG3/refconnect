package radio

import (
	"fmt"
	"io"
	"sync"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"go.bug.st/serial"
)

// SerialRadio implements RadioInterface over a physical serial port.
type SerialRadio struct {
	mu      sync.Mutex
	port    serial.Port
	cfg     Config
	open    bool
	pttRTS  bool

	hdrCh chan dstar.DVHeader
	frmCh chan dstar.DVFrame
	stopCh chan struct{}
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

	mode := &serial.Mode{
		BaudRate: cfg.BaudRate,
		DataBits: cfg.DataBits,
		StopBits: cfg.StopBits,
		Parity:   cfg.Parity,
	}
	p, err := serial.Open(cfg.Port, mode)
	if err != nil {
		return fmt.Errorf("radio: open %s: %w", cfg.Port, err)
	}
	r.port = p
	r.cfg = cfg
	r.open = true
	r.stopCh = make(chan struct{})

	go r.readLoop()
	return nil
}

func (r *SerialRadio) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open {
		return nil
	}
	close(r.stopCh)
	err := r.port.Close()
	r.open = false
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
	return writeHeader(r.port, hdr)
}

func (r *SerialRadio) SendFrame(f dstar.DVFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open {
		return fmt.Errorf("radio: port not open")
	}
	return writeFrame(r.port, f)
}

func (r *SerialRadio) RxHeaders() <-chan dstar.DVHeader { return r.hdrCh }
func (r *SerialRadio) RxFrames() <-chan dstar.DVFrame   { return r.frmCh }

func (r *SerialRadio) PTT(on bool) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.open {
		return fmt.Errorf("radio: port not open")
	}
	if !r.cfg.PTTViaRTS {
		return nil
	}
	return r.port.SetRTS(on)
}

// readLoop runs in a background goroutine, feeding received frames into channels.
func (r *SerialRadio) readLoop() {
	for {
		select {
		case <-r.stopCh:
			return
		default:
		}
		err := readNext(r.port, r.hdrCh, r.frmCh)
		if err == io.EOF {
			return
		}
		// On non-fatal errors (framing glitches), log and continue.
		// The caller can check IsOpen(); if the port was closed the loop exits.
		_ = err
	}
}
