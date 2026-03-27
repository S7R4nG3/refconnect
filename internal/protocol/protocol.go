// Package protocol defines the Reflector interface implemented by each
// D-STAR reflector protocol (DExtra, DPlus, XLX).
package protocol

import "github.com/S7R4nG3/refconnect/internal/dstar"

// Protocol identifies which reflector linking protocol to use.
type Protocol string

const (
	ProtoDExtra Protocol = "DExtra"
	ProtoDPlus  Protocol = "DPlus"
	ProtoXLX    Protocol = "XLX"
)

// Config holds the parameters needed to connect to a reflector.
type Config struct {
	Host     string
	Port     uint16
	Module   byte   // 'A'–'Z'
	MyCall   string // local station callsign (8 chars, space-padded)
	Protocol Protocol
}

// LinkState represents the current state of a reflector link.
type LinkState int

const (
	StateDisconnected LinkState = iota
	StateConnecting
	StateConnected
	StateError
)

func (s LinkState) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting"
	case StateConnected:
		return "Connected"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

// Event is sent on the Events() channel on state changes or notable link activity.
type Event struct {
	State   LinkState
	Message string
}

// Reflector is the interface that all protocol clients must satisfy.
// All methods are safe to call from multiple goroutines.
type Reflector interface {
	// Connect initiates a link to the reflector.  It returns after the
	// connection handshake completes or fails; use Events() for async state.
	Connect(cfg Config) error
	// Disconnect gracefully unlinks from the reflector.
	Disconnect() error
	// State returns the current link state.
	State() LinkState

	// SendHeader transmits a DV header to the reflector.
	SendHeader(hdr dstar.DVHeader) error
	// SendFrame transmits a voice frame to the reflector.
	SendFrame(f dstar.DVFrame) error

	// RxHeaders returns received DV headers from the reflector.
	RxHeaders() <-chan dstar.DVHeader
	// RxFrames returns received DV voice frames from the reflector.
	RxFrames() <-chan dstar.DVFrame

	// Events returns link state changes and keepalive notifications.
	Events() <-chan Event
}
