// Package router connects a RadioInterface to a Reflector, forwarding DV
// frames in both directions. It is the concurrency hub of the application.
package router

import (
	"log"
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

	// MyCall is the local 8-char callsign with module suffix (e.g. "KR4GCQ D").
	// Used to rewrite RPT1 in outbound headers.
	MyCall string

	// ReflectorModule is the target module on the reflector (e.g. 'C').
	ReflectorModule byte

	// ReflectorCall is the reflector's D-STAR callsign (e.g. "REF001").
	// Combined with ReflectorModule to form RPT2 (e.g. "REF001 C").
	ReflectorCall string
}

// Router routes DV frames between a radio and a reflector.
type Router struct {
	radio     radio.RadioInterface
	reflector protocol.Reflector
	cfg       Config

	eventCh      chan Event
	stopCh       chan struct{}
	once         sync.Once
	txFrameCount int // diagnostic counter for TX voice frames per transmission
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

// rewriteHeaderForReflector rewrites the routing fields in a header received
// from the radio before forwarding to the reflector. The radio sends headers
// with RPT1/RPT2 set to "DIRECT  " (or local repeater); the reflector needs
// proper gateway routing to register the transmission.
func (rt *Router) rewriteHeaderForReflector(hdr *dstar.DVHeader) {
	if rt.cfg.MyCall == "" {
		return
	}
	// RPT1 = local gateway callsign (e.g. "KR4GCQ G").
	// Per ircddbGateway DPlusHandler: header.setRepeaters(m_callsign, m_reflector)
	// where m_callsign is the local gateway call used as RPT1.
	rpt1 := []byte(dstar.PadCallsign(rt.cfg.MyCall, 8))
	rpt1[7] = 'G'
	hdr.RPT1 = string(rpt1)
	// RPT2 = reflector callsign + module (e.g. "REF001 C").
	// The reflector validates that RPT2 matches its own callsign.
	rpt2 := []byte(dstar.PadCallsign(rt.cfg.ReflectorCall, 8))
	rpt2[7] = rt.cfg.ReflectorModule
	hdr.RPT2 = string(rpt2)
	// URCALL = "CQCQCQ  " for a general call
	hdr.YourCall = dstar.CQCall
	// Flag1 = 0x00 for a forwarded (not TX-direction) header
	hdr.Flag1 = 0x00
	log.Printf("router: rewrite header for reflector: RPT1=%q RPT2=%q URCALL=%q MYCALL=%q",
		hdr.RPT1, hdr.RPT2, hdr.YourCall, hdr.MyCall)
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
			rt.rewriteHeaderForReflector(&hdr)
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
			rt.txFrameCount++
			if rt.txFrameCount == 1 {
				log.Printf("router: first TX voice frame (seq=%d)", frm.Seq)
			}
			if frm.End {
				log.Printf("router: TX end-of-stream (seq=%d, total frames=%d)", frm.Seq, rt.txFrameCount)
				rt.txFrameCount = 0
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
			log.Printf("router: RX header from reflector: MYCALL=%q URCALL=%q RPT1=%q RPT2=%q",
				hdr.MyCall, hdr.YourCall, hdr.RPT1, hdr.RPT2)
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
