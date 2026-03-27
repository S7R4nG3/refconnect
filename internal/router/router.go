// Package router connects a RadioInterface to a Reflector, forwarding DV
// frames in both directions. It is the concurrency hub of the application.
package router

import (
	"sync"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"github.com/S7R4nG3/refconnect/internal/protocol"
	"github.com/S7R4nG3/refconnect/internal/radio"
)

// Direction indicates which way a RouterEvent flowed.
type Direction int

const (
	DirTX Direction = iota // radio → reflector (transmit)
	DirRX                  // reflector → radio (receive)
)

// Event is emitted on the Events channel for every notable routing occurrence.
type Event struct {
	Direction Direction
	Header    *dstar.DVHeader // non-nil when a new transmission starts
	Frame     *dstar.DVFrame  // non-nil for voice frames
	Err       error
}

// Config controls router behaviour.
type Config struct {
	// DropTXWhenDisconnected silently drops outbound frames when the reflector
	// link is not in StateConnected.
	DropTXWhenDisconnected bool
}

// Router routes DV frames between a radio and a reflector.
type Router struct {
	radio     radio.RadioInterface
	reflector protocol.Reflector
	cfg       Config

	eventCh chan Event
	stopCh  chan struct{}
	once    sync.Once
}

// New creates a Router.  Call Start to begin routing.
func New(r radio.RadioInterface, ref protocol.Reflector, cfg Config) *Router {
	return &Router{
		radio:     r,
		reflector: ref,
		cfg:       cfg,
		eventCh:   make(chan Event, 16),
		stopCh:    make(chan struct{}),
	}
}

// Events returns the channel of routing events consumed by the UI.
func (rt *Router) Events() <-chan Event { return rt.eventCh }

// Start spawns the routing goroutines. It is idempotent.
func (rt *Router) Start() {
	rt.once.Do(func() {
		go rt.txLoop() // radio → reflector
		go rt.rxLoop() // reflector → radio
	})
}

// Stop signals the routing goroutines to exit.
func (rt *Router) Stop() {
	close(rt.stopCh)
}

// txLoop reads from the radio and forwards to the reflector (TX path).
func (rt *Router) txLoop() {
	for {
		select {
		case <-rt.stopCh:
			return
		case hdr, ok := <-rt.radio.RxHeaders():
			if !ok {
				return
			}
			if rt.cfg.DropTXWhenDisconnected && rt.reflector.State() != protocol.StateConnected {
				continue
			}
			if err := rt.reflector.SendHeader(hdr); err != nil {
				rt.emit(Event{Direction: DirTX, Err: err})
				continue
			}
			h := hdr
			rt.emit(Event{Direction: DirTX, Header: &h})

		case frm, ok := <-rt.radio.RxFrames():
			if !ok {
				return
			}
			if rt.cfg.DropTXWhenDisconnected && rt.reflector.State() != protocol.StateConnected {
				continue
			}
			if err := rt.reflector.SendFrame(frm); err != nil {
				rt.emit(Event{Direction: DirTX, Err: err})
				continue
			}
			f := frm
			rt.emit(Event{Direction: DirTX, Frame: &f})
		}
	}
}

// rxLoop reads from the reflector and forwards to the radio (RX path).
func (rt *Router) rxLoop() {
	for {
		select {
		case <-rt.stopCh:
			return
		case hdr, ok := <-rt.reflector.RxHeaders():
			if !ok {
				return
			}
			if rt.radio.IsOpen() {
				rt.radio.SendHeader(hdr) //nolint:errcheck
			}
			h := hdr
			rt.emit(Event{Direction: DirRX, Header: &h})

		case frm, ok := <-rt.reflector.RxFrames():
			if !ok {
				return
			}
			if rt.radio.IsOpen() {
				rt.radio.SendFrame(frm) //nolint:errcheck
			}
			f := frm
			rt.emit(Event{Direction: DirRX, Frame: &f})
		}
	}
}

func (rt *Router) emit(e Event) {
	select {
	case rt.eventCh <- e:
	default: // drop if UI is not consuming fast enough
	}
}
