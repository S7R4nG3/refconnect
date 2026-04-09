package radio

import (
	"fmt"
	"sync"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// MockRadio implements RadioInterface as an in-process loopback.
// Anything sent via SendHeader/SendFrame is immediately available on RxHeaders/RxFrames.
// Use this for unit testing and development without physical hardware.
type MockRadio struct {
	mu     sync.Mutex
	open   bool
	ptt    bool
	hdrCh  chan dstar.DVHeader
	frmCh  chan dstar.DVFrame

	// LogTX records transmitted frames for test assertions.
	LogTX []dstar.DVFrame
}

// NewMockRadio returns an already-open MockRadio (no port needed).
func NewMockRadio() *MockRadio {
	m := &MockRadio{
		hdrCh: make(chan dstar.DVHeader, 8),
		frmCh: make(chan dstar.DVFrame, 32),
	}
	m.open = true
	return m
}

func (m *MockRadio) Open(_ Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.open = true
	return nil
}

func (m *MockRadio) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.open = false
	return nil
}

func (m *MockRadio) IsOpen() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.open
}

func (m *MockRadio) SendHeader(hdr dstar.DVHeader) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.open {
		return fmt.Errorf("mock radio: not open")
	}
	// Loopback: re-inject into the receive channel.
	select {
	case m.hdrCh <- hdr:
	default:
	}
	return nil
}

func (m *MockRadio) SendFrame(f dstar.DVFrame) error {
	m.mu.Lock()
	open := m.open
	m.mu.Unlock()
	if !open {
		return fmt.Errorf("mock radio: not open")
	}
	m.mu.Lock()
	m.LogTX = append(m.LogTX, f)
	m.mu.Unlock()
	select {
	case m.frmCh <- f:
	default:
	}
	return nil
}

func (m *MockRadio) RxHeaders() <-chan dstar.DVHeader { return m.hdrCh }
func (m *MockRadio) RxFrames() <-chan dstar.DVFrame   { return m.frmCh }

func (m *MockRadio) PTT(on bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ptt = on
	return nil
}

// PTTState returns the current simulated PTT state (useful in tests).
func (m *MockRadio) PTTState() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ptt
}

// InjectHeader pushes a header into the receive channel as if it arrived from hardware.
// Non-blocking; drops the header if the channel is full.
func (m *MockRadio) InjectHeader(hdr dstar.DVHeader) {
	select {
	case m.hdrCh <- hdr:
	default:
	}
}

// InjectFrame pushes a voice frame into the receive channel as if it arrived from hardware.
// Non-blocking; drops the frame if the channel is full.
func (m *MockRadio) InjectFrame(f dstar.DVFrame) {
	select {
	case m.frmCh <- f:
	default:
	}
}
