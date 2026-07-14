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

	// TXText is a D-STAR slow data text message (up to 20 chars) injected
	// into every outgoing superframe. It appears as the "User Message" on
	// reflector status pages and ircDDB Last Heard listings.
	TXText string
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

	// slowDump accumulates the descrambled slow-data payload of the current
	// transmission for diagnostics. Dumped (as hex + printable ASCII) at
	// end-of-stream when no GPS fix was decoded, so we can see exactly what
	// the radio embedded — a $$CRC sentence, raw NMEA, a text message, or
	// nothing. Reset on each header.
	slowDump    []byte
	gotFixThisTX bool

	// txText holds pre-scrambled slow data for a 20-char D-STAR text
	// message injected into every outgoing superframe. This appears as
	// the "User Message" on reflector status pages.
	txText [dstar.MaxSeq + 1][3]byte
}

// New creates a Router.  Call Start to begin routing.
func New(r radio.RadioInterface, ref protocol.Reflector, cfg Config) *Router {
	rt := &Router{
		radio:     r,
		reflector: ref,
		cfg:       cfg,
		eventCh:   make(chan Event, 16),
		stopCh:    make(chan struct{}),
		gps:       &aprs.Cache{},
	}
	if cfg.TXText != "" {
		rt.txText = dstar.EncodeTextMessage(cfg.TXText)
		log.Printf("router: TX text message: %q", cfg.TXText)
	}
	return rt
}

// GPS returns the shared GPS cache updated from the radio's outbound
// slow-data stream. Callers should only read from the returned cache.
func (rt *Router) GPS() *aprs.Cache { return rt.gps }

// SendBeacon synthesises a DPRS transmission and writes it to the
// reflector. It blocks until the current user voice transmission (if any)
// completes so frames never interleave. Returns the DVHeader that was
// sent (for ircDDB announcement) and any error.
func (rt *Router) SendBeacon(dprsSentence string) (dstar.DVHeader, error) {
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
			rt.slowDump = rt.slowDump[:0]
			rt.gotFixThisTX = false
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
				// Link went down mid-transmission: drop this frame, but still
				// release the TX claim at end-of-stream. Otherwise txMu stays
				// locked forever and every subsequent SendBeacon blocks.
				if frm.End && rt.txBusy {
					rt.txFrameCount = 0
					rt.txMu.Unlock()
					rt.txBusy = false
				}
				continue
			}
			rt.txFrameCount++
			if rt.txFrameCount == 1 {
				log.Printf("router: first TX voice frame (seq=%d)", frm.Seq)
			}
			// Diagnostic: dump the raw slow-data bytes for the first superframe
			// of each transmission. The scrambler in dstar/slowdata.go is now
			// verified against the ground-truth pcaps (key 70 4F 93), so decode
			// below should work; keep this log until radio GPS is confirmed
			// on-air, then gate it behind a debug flag or remove (see GPS.md).
			if rt.txFrameCount <= dstar.MaxSeq+1 {
				log.Printf("router: TX slow-data seq=%2d raw=% 02X", frm.Seq, frm.SlowData)
			}
			// Accumulate the descrambled slow-data payload across the whole
			// transmission so end-of-stream can show what the radio embedded
			// (see slowDump). Sync frames carry no payload — skip them.
			if frm.SlowData != dstar.SyncSlowData {
				d := dstar.ScrambleSlowData(frm.SlowData, 1) // constant-key descramble
				rt.slowDump = append(rt.slowDump, d[:]...)
			}
			// Sniff slow-data for DPRS sentences before forwarding.
			for _, sentence := range rt.dprsDec.Feed(frm.SlowData, frm.Seq) {
				rt.handleDPRSSentence(sentence)
			}
			// Replace slow data with our text message so reflector
			// dashboards and ircDDB show it as the "User Message".
			if rt.cfg.TXText != "" {
				frm.SlowData = rt.txText[frm.Seq%(dstar.MaxSeq+1)]
			}
			if frm.End {
				log.Printf("router: TX end-of-stream (seq=%d, total frames=%d)", frm.Seq, rt.txFrameCount)
				// If nothing decoded, dump the whole descrambled slow-data
				// payload so we can see what the radio actually embedded
				// (a $$CRC sentence, raw NMEA, a text message, or idle fill).
				if !rt.gotFixThisTX && len(rt.slowDump) > 0 {
					log.Printf("router: no GPS decoded this TX — descrambled slow-data (%d bytes):\n  ascii: %q\n  hex:   % 02X",
						len(rt.slowDump), printableASCII(rt.slowDump), rt.slowDump)
				}
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

// printableASCII renders a byte slice for logging, replacing non-printable
// bytes with '.' so a slow-data dump reads cleanly (e.g. "$$CRC..." or
// "$GPRMC..." stand out amid idle fill).
func printableASCII(b []byte) string {
	out := make([]byte, len(b))
	for i, c := range b {
		if c >= 0x20 && c < 0x7f {
			out[i] = c
		} else {
			out[i] = '.'
		}
	}
	return string(out)
}

// handleDPRSSentence validates a slow-data DPRS sentence, parses the
// embedded position if possible, and updates the GPS cache.
func (rt *Router) handleDPRSSentence(sentence string) {
	log.Printf("router: DPRS candidate sentence: %q", sentence)
	payload, ok := aprs.ValidateDPRS(sentence)
	if !ok {
		// Accept NMEA-only sentences too — some radios emit raw $GPRMC/$GPGGA
		// without the $$CRC wrapper.
		log.Printf("router: DPRS $$CRC framing/checksum not valid — trying raw payload as NMEA/TNC2")
		payload = sentence
	}
	pos, ok := aprs.ParsePosition(payload)
	if !ok {
		log.Printf("router: DPRS position parse failed for %q", payload)
		return
	}
	rt.gps.Set(pos)
	rt.gotFixThisTX = true
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
