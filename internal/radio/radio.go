// Package radio abstracts the serial/UART interface to a D-STAR capable radio.
// The RadioInterface allows the rest of the application to send and receive
// D-STAR DV headers and voice frames without knowing the underlying transport.
package radio

import (
	"github.com/S7R4nG3/refconnect/internal/dstar"
	"go.bug.st/serial"
)

// Config holds the parameters needed to open a serial port.
type Config struct {
	Port      string
	BaudRate  int
	DataBits  int
	StopBits  serial.StopBits
	Parity    serial.Parity
	PTTViaRTS bool // assert RTS pin for PTT when true
}

// RadioInterface is the contract between the serial layer and the router.
// Implementations include SerialRadio (real hardware) and MockRadio (loopback).
type RadioInterface interface {
	// Open initialises the serial port with the given config.
	Open(cfg Config) error
	// Close releases the serial port.
	Close() error
	// IsOpen reports whether the port is currently open.
	IsOpen() bool

	// SendHeader transmits a DV header burst to the radio.
	SendHeader(hdr dstar.DVHeader) error
	// SendFrame transmits a single DV voice frame to the radio.
	SendFrame(f dstar.DVFrame) error

	// RxHeaders returns a channel of headers received from the radio.
	// The channel is closed when the radio is closed.
	RxHeaders() <-chan dstar.DVHeader
	// RxFrames returns a channel of voice frames received from the radio.
	// The channel is closed when the radio is closed.
	RxFrames() <-chan dstar.DVFrame

	// PTT asserts (on=true) or releases (on=false) the push-to-talk signal.
	// When PTTViaRTS is true in Config, this toggles the RTS serial line.
	PTT(on bool) error
}
