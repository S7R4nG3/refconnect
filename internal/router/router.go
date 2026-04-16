// Package router connects a RadioInterface to a Reflector, forwarding DV
// frames in both directions. It is the concurrency hub of the application.
package router

import (
	"log"
	"sync"

	"github.com/S7R4nG3/refconnect/internal/aprs"
	"github.com/S7R4nG3/refconnect/internal/beacon"
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
	wg           sync.WaitGroup // tracks txLoop + rxLoop
	once         sync.Once
	txFrameCount int // diagnostic counter for TX voice frames per transmission

	// txMu serializes user-voice TX (txLoop) against synthetic beacon TX
	// (SendBeacon). It is held from header through End-of-stream so beacons
	// never interleave mid-transmission.
	txMu    sync.Mutex
	txBusy  bool

	// gps caches the last GPS fix extracted from the radio's outbound
	// slow-data stream. Populated whenever the user keys up a radio with
	// GPS data enabled.
	gps     *aprs.Cache
	dprsDec dstar.DPRSDecoder
}

// New creates a Router.  Call Start to begin routing.
func New(r radio.RadioInterface, ref protocol.Reflector, cfg Config) *Router {
	return &Router{
		radio:     r,
		reflector: ref,
		cfg:       cfg,
		eventCh:   make(chan Event, 16),
		stopCh:    make(chan struct{}),
		gps:       &aprs.Cache{},
	}
}

// GPS returns the shared GPS cache updated from the radio's outbound
// slow-data stream. Callers should only read from the returned cache.
func (rt *Router) GPS() *aprs.Cache { return rt.gps }

// SendBeacon synthesises a DPRS transmission and writes it to the
// reflector. It blocks until the current user voice transmission (if any)
// completes so frames never interleave. Returns an error if no reflector
// is connected.
func (rt *Router) SendBeacon(dprsSentence string) error {
	rt.txMu.Lock()
	defer rt.txMu.Unlock()
	return beacon.Send(rt.reflector, rt.cfg.MyCall,
		rt.cfg.MyCall, reflectorRPT2(rt.cfg.ReflectorCall, rt.cfg.ReflectorModule),
		dprsSentence)
}

// reflectorRPT2 builds the 8-char RPT2 string from reflector call + module.
func reflectorRPT2(refl string, mod byte) string {
	b := []byte(dstar.PadCallsign(refl, 8))
	b[7] = mod
	return string(b)
}

// Events returns the channel of routing events consumed by the UI.
func (rt *Router) Events() <-chan Event { return rt.eventCh }

// Start spawns the routing goroutines. It is idempotent.
func (rt *Router) Start() {
	rt.once.Do(func() {
		rt.wg.Add(2)
		go rt.txLoop() // radio → reflector
		go rt.rxLoop() // reflector → radio
	})
}

// Stop signals the routing goroutines to exit, waits for them to finish,
// then closes the event channel so consumers (UI) unblock.
func (rt *Router) Stop() {
	select {
	case <-rt.stopCh:
		return // already stopped
	default:
		close(rt.stopCh)
	}
	rt.wg.Wait()
	close(rt.eventCh)
}

// rewriteHeaderForReflector rewrites the routing fields in a header received
// from the radio before forwarding to the reflector. The radio sends headers
// with RPT1/RPT2 set to "DIRECT  " (or local repeater); the reflector needs
// proper gateway routing to register the transmission.
func (rt *Router) rewriteHeaderForReflector(hdr *dstar.DVHeader) {
	if rt.cfg.MyCall == "" {
		return
	}
	// RPT1 = local gateway callsign (e.g. "KR4GCQ  " or "KR4GCQ G").
	// MyCall already encodes the correct 8th-character suffix chosen in the UI;
	// overriding it here would mismatch the local module sent in the connect packet
	// and cause XRF reflectors to silently drop the header.
	hdr.RPT1 = dstar.PadCallsign(rt.cfg.MyCall, 8)
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
// It also sniffs slow-data for DPRS position reports and feeds them into
// the shared GPS cache, and holds txMu across a full transmission so
// synthetic beacons can't interleave.
func (rt *Router) txLoop() {
	defer rt.wg.Done()
	for {
		select {
		case <-rt.stopCh:
			if rt.txBusy {
				rt.txMu.Unlock()
				rt.txBusy = false
			}
			return
		case hdr, ok := <-rt.radio.RxHeaders():
			if !ok {
				return
			}
			if rt.cfg.DropTXWhenDisconnected && rt.reflector.State() != protocol.StateConnected {
				continue
			}
			// Claim the TX path for the duration of this transmission.
			if !rt.txBusy {
				rt.txMu.Lock()
				rt.txBusy = true
			}
			rt.dprsDec.Reset()
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
			// Sniff slow-data for DPRS sentences before forwarding.
			for _, sentence := range rt.dprsDec.Feed(frm.SlowData, frm.Seq) {
				rt.handleDPRSSentence(sentence)
			}
			if frm.End {
				log.Printf("router: TX end-of-stream (seq=%d, total frames=%d)", frm.Seq, rt.txFrameCount)
				rt.txFrameCount = 0
			}
			if err := rt.reflector.SendFrame(frm); err != nil {
				rt.emit(Event{Direction: DirTX, Err: err})
				if frm.End && rt.txBusy {
					rt.txMu.Unlock()
					rt.txBusy = false
				}
				continue
			}
			f := frm
			rt.emit(Event{Direction: DirTX, Frame: &f})
			if frm.End && rt.txBusy {
				rt.txMu.Unlock()
				rt.txBusy = false
			}
		}
	}
}

// handleDPRSSentence validates a slow-data DPRS sentence, parses the
// embedded position if possible, and updates the GPS cache.
func (rt *Router) handleDPRSSentence(sentence string) {
	payload, ok := aprs.ValidateDPRS(sentence)
	if !ok {
		// Accept NMEA-only sentences too — some radios emit raw $GPRMC/$GPGGA
		// without the $$CRC wrapper.
		payload = sentence
	}
	pos, ok := aprs.ParsePosition(payload)
	if !ok {
		return
	}
	rt.gps.Set(pos)
	log.Printf("router: GPS fix from radio: %.5f, %.5f", pos.Lat, pos.Lon)
}

// rxLoop reads from the reflector and forwards to the radio (RX path).
func (rt *Router) rxLoop() {
	defer rt.wg.Done()
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
				if err := rt.radio.SendHeader(hdr); err != nil {
					rt.emit(Event{Direction: DirRX, Err: err})
				}
			}
			h := hdr
			rt.emit(Event{Direction: DirRX, Header: &h})

		case frm, ok := <-rt.reflector.RxFrames():
			if !ok {
				return
			}
			if rt.radio.IsOpen() {
				if err := rt.radio.SendFrame(frm); err != nil {
					rt.emit(Event{Direction: DirRX, Err: err})
				}
			}
			f := frm
			rt.emit(Event{Direction: DirRX, Frame: &f})
		}
	}
}

func (rt *Router) emit(e Event) {
	select {
	case <-rt.stopCh:
		return // stopped; eventCh is closed
	case rt.eventCh <- e:
	default: // drop if UI is not consuming fast enough
	}
}
