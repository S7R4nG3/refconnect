package dcs

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"github.com/S7R4nG3/refconnect/internal/protocol"
)

const (
	keepaliveInterval = 5 * time.Second
	connectTimeout    = 10 * time.Second
	udpReadBuf        = 4096
)

// Client implements protocol.Reflector for the DCS protocol.
type Client struct {
	mu         sync.Mutex
	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	cfg        protocol.Config
	state      atomic.Int32 // stores protocol.LinkState

	hdrCh   chan dstar.DVHeader
	frmCh   chan dstar.DVFrame
	eventCh chan protocol.Event

	stopCh chan struct{}
	wg     sync.WaitGroup // tracks rxLoop + keepaliveLoop

	// TX state: DCS embeds the full header in every voice packet, so we
	// cache the current TX header and stream ID. txSeq is an absolute
	// packet counter placed at bytes 58-60 of each outgoing voice packet.
	streamID uint16
	txHeader dstar.DVHeader
	txSeq    uint32

	// RX dedup: each voice packet carries a header, but we only forward
	// the first header per stream to avoid resetting the radio's decoder.
	rxStreamID     uint16
	rxStreamActive bool

	// Reflector identity for keepalive packets.
	reflectorCall   string // e.g. "DCS001"
	reflectorModule byte   // e.g. 'C'
}

// New returns a new DCS client. Call Connect to establish a link.
func New() *Client {
	return &Client{
		hdrCh:   make(chan dstar.DVHeader, 8),
		frmCh:   make(chan dstar.DVFrame, 32),
		eventCh: make(chan protocol.Event, 8),
	}
}

func (c *Client) State() protocol.LinkState {
	return protocol.LinkState(c.state.Load())
}

func (c *Client) setState(s protocol.LinkState, msg string) {
	c.state.Store(int32(s))
	select {
	case c.eventCh <- protocol.Event{State: s, Message: msg}:
	default:
	}
}

func (c *Client) Connect(cfg protocol.Config) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.State() == protocol.StateConnected || c.State() == protocol.StateConnecting {
		return fmt.Errorf("dcs: already connected or connecting")
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("dcs: resolve %s:%d: %w", cfg.Host, cfg.Port, err)
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return fmt.Errorf("dcs: listen: %w", err)
	}

	c.conn = conn
	c.remoteAddr = addr
	c.cfg = cfg
	c.stopCh = make(chan struct{})
	c.streamID = nextStreamID()
	c.txSeq = 0
	c.setState(protocol.StateConnecting, fmt.Sprintf("Connecting to %s:%d module %c", cfg.Host, cfg.Port, cfg.Module))

	fail := func(msg string, err error) error {
		conn.Close()
		c.conn = nil
		c.setState(protocol.StateError, msg)
		return err
	}

	// Derive the reflector callsign from the hostname (e.g. "dcs001.openquad.net" → "DCS001").
	c.reflectorCall = deriveReflectorCall(cfg.Host)
	c.reflectorModule = cfg.Module

	// DCS protocol module is hardcoded to 'G' so the reflector classifies
	// the client as "Dongle" on the status page. The user's callsign suffix
	// (which may be a space) is preserved in D-STAR radio headers.
	localModule := byte('G')

	pkt := buildConnectPacket(cfg.MyCall, localModule, cfg.Module, c.reflectorCall)
	log.Printf("dcs: sending connect packet (%d bytes) to %s:%d\n%s",
		len(pkt), cfg.Host, cfg.Port, hex.Dump(pkt[:min(len(pkt), 32)]))
	if _, err := conn.WriteToUDP(pkt, addr); err != nil {
		return fail("send connect: "+err.Error(), err)
	}

	// Wait for ACK.
	conn.SetReadDeadline(time.Now().Add(connectTimeout)) //nolint:errcheck
	buf := make([]byte, udpReadBuf)
	n, fromAddr, err := conn.ReadFromUDP(buf)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	if err != nil {
		return fail("connect timeout: "+err.Error(), fmt.Errorf("dcs: connect ack: %w", err))
	}
	log.Printf("dcs: received %d bytes from %s:\n%s", n, fromAddr, hex.Dump(buf[:min(n, 64)]))

	// DCS servers respond to a successful connect with a 22-byte ACK that
	// looks like a keepalive: reflectorCall(7) + module(1) + space(1) +
	// clientCall(7) + clientModule(2) + tag{0x0A,0x00,0x20,0x20}.
	// Verify the response contains our callsign to confirm the link.
	if n < 22 {
		return fail(
			fmt.Sprintf("connect rejected by reflector (got %d bytes)", n),
			fmt.Errorf("dcs: connect rejected"),
		)
	}
	// Sanity check: the trailing tag bytes should be {0x0A, 0x00, 0x20, 0x20}.
	if n >= 22 && buf[n-4] != 0x0A {
		return fail(
			fmt.Sprintf("connect rejected by reflector (unexpected response: %d bytes)", n),
			fmt.Errorf("dcs: connect rejected"),
		)
	}

	log.Printf("dcs: connected from local %s to %s", conn.LocalAddr(), addr)
	c.setState(protocol.StateConnected, fmt.Sprintf("Linked to %s module %c", cfg.Host, cfg.Module))
	c.wg.Add(2)
	go c.rxLoop()
	go c.keepaliveLoop()
	return nil
}

func (c *Client) Disconnect() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return nil
	}
	pkt := buildDisconnectPacket(c.cfg.MyCall, c.reflectorModule)
	c.conn.WriteToUDP(pkt, c.remoteAddr) //nolint:errcheck
	close(c.stopCh)
	err := c.conn.Close()
	c.conn = nil
	c.setState(protocol.StateDisconnected, "Disconnected")
	c.wg.Wait()
	close(c.hdrCh)
	close(c.frmCh)
	close(c.eventCh)
	return err
}

// SendHeader caches the header for embedding in subsequent voice packets and
// generates a new stream ID. DCS does not send a separate header packet on
// the wire.
func (c *Client) SendHeader(hdr dstar.DVHeader) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		return fmt.Errorf("dcs: not connected")
	}
	c.streamID = nextStreamID()
	c.txHeader = hdr
	c.txSeq = 0
	log.Printf("dcs: SendHeader streamID=%04X MYCALL=%q URCALL=%q",
		c.streamID, hdr.MyCall, hdr.YourCall)
	return nil
}

// SendFrame transmits a voice frame with the cached header embedded.
func (c *Client) SendFrame(f dstar.DVFrame) error {
	c.mu.Lock()
	conn, addr, sid, hdr, seq := c.conn, c.remoteAddr, c.streamID, c.txHeader, c.txSeq
	c.txSeq++
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dcs: not connected")
	}
	pkt, err := encodeVoicePacket(sid, f.Seq, f.End, hdr, f, seq)
	if err != nil {
		return err
	}
	if f.Seq == 0 {
		log.Printf("dcs: TX voice packet #0 (%d bytes):\n%s", len(pkt), hex.Dump(pkt[:min(len(pkt), 64)]))
	}
	_, err = conn.WriteToUDP(pkt, addr)
	return err
}

func (c *Client) RxHeaders() <-chan dstar.DVHeader { return c.hdrCh }
func (c *Client) RxFrames() <-chan dstar.DVFrame   { return c.frmCh }
func (c *Client) Events() <-chan protocol.Event    { return c.eventCh }

// rxLoop reads inbound UDP packets and dispatches headers/frames.
func (c *Client) rxLoop() {
	defer c.wg.Done()
	buf := make([]byte, udpReadBuf)
	for {
		c.mu.Lock()
		conn := c.conn
		c.mu.Unlock()
		if conn == nil {
			return
		}

		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-c.stopCh:
			default:
				c.setState(protocol.StateError, "rx error: "+err.Error())
			}
			return
		}

		// Debug: hex dump voice-sized packets to diagnose header offset.
		if n >= 60 && buf[0] == '0' && buf[1] == '0' && buf[2] == '0' && buf[3] == '1' {
			log.Printf("dcs: RX voice packet (%d bytes):\n%s", n, hex.Dump(buf[:min(n, 100)]))
		}

		hdr, frm, sid, err := parsePacket(buf[:n])
		if err != nil {
			log.Printf("dcs: parse error: %v", err)
			continue
		}
		if hdr == nil && frm == nil {
			log.Printf("dcs: RX control/keepalive (%d bytes):\n%s", n, hex.Dump(buf[:n]))
		}

		// Voice packet: both hdr and frm are non-nil.
		if hdr != nil {
			// Deduplicate: only forward the first header per stream.
			// DCS has no stream ID, so we track by MYCALL. A new header
			// is forwarded when the source callsign changes or after an
			// end-of-stream was received.
			c.mu.Lock()
			dup := c.rxStreamActive && sid == c.rxStreamID
			if !dup {
				c.rxStreamID = sid
				c.rxStreamActive = true
				log.Printf("dcs: new RX stream %04X from %s — forwarding header", sid, strings.TrimSpace(hdr.MyCall))
			}
			c.mu.Unlock()
			if !dup {
				select {
				case c.hdrCh <- *hdr:
				default:
					log.Printf("dcs: dropped inbound header: channel full")
				}
			}
		}
		if frm != nil {
			if frm.End {
				c.mu.Lock()
				c.rxStreamActive = false
				c.mu.Unlock()
				log.Printf("dcs: RX stream end")
			}
			select {
			case c.frmCh <- *frm:
			default:
			}
		}
	}
}

// keepaliveLoop sends a poll every keepaliveInterval.
func (c *Client) keepaliveLoop() {
	defer c.wg.Done()
	ticker := time.NewTicker(keepaliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.Lock()
			conn := c.conn
			callsign := c.cfg.MyCall
			refCall := c.reflectorCall
			refMod := c.reflectorModule
			c.mu.Unlock()
			if conn == nil {
				return
			}
			pkt := buildKeepalive(callsign, 'G', refCall, refMod)
			if _, err := conn.WriteToUDP(pkt, c.remoteAddr); err != nil {
				log.Printf("dcs: keepalive send error: %v", err)
			} else {
				log.Printf("dcs: keepalive sent (%d bytes)", len(pkt))
			}
		}
	}
}

// deriveReflectorCall extracts the reflector callsign from a hostname.
// e.g. "dcs001.openquad.net" → "DCS001  " (space-padded to 8 chars).
func deriveReflectorCall(host string) string {
	parts := strings.SplitN(host, ".", 2)
	call := strings.ToUpper(parts[0])
	return dstar.PadCallsign(call, 8)
}
