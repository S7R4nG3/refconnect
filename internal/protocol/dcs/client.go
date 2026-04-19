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
	// cache the current TX header and stream ID.
	streamID uint16
	txHeader dstar.DVHeader

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

	// Determine the local module from the callsign suffix.
	cs := dstar.PadCallsign(cfg.MyCall, 8)
	localModule := cs[7]
	if localModule == ' ' {
		localModule = 'A'
	}

	pkt := buildConnectPacket(cfg.MyCall, localModule, cfg.Module)
	log.Printf("dcs: sending connect packet (%d bytes) to %s:%d", len(pkt), cfg.Host, cfg.Port)
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

	// DCS servers echo the connect packet back with the same length (519 bytes)
	// on success. A shorter or different response indicates rejection.
	// Be permissive: accept any response >= 100 bytes as an ACK, since
	// different DCS server implementations may vary.
	if n < 100 {
		return fail(
			fmt.Sprintf("connect rejected by reflector (got %d bytes)", n),
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
	log.Printf("dcs: SendHeader streamID=%04X MYCALL=%q URCALL=%q",
		c.streamID, hdr.MyCall, hdr.YourCall)
	return nil
}

// SendFrame transmits a voice frame with the cached header embedded.
func (c *Client) SendFrame(f dstar.DVFrame) error {
	c.mu.Lock()
	conn, addr, sid, hdr := c.conn, c.remoteAddr, c.streamID, c.txHeader
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dcs: not connected")
	}
	pkt, err := encodeVoicePacket(sid, f.Seq, f.End, hdr, f)
	if err != nil {
		return err
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

		hdr, frm, sid, err := parsePacket(buf[:n])
		if err != nil {
			log.Printf("dcs: parse error: %v", err)
			continue
		}

		// Voice packet: both hdr and frm are non-nil.
		if hdr != nil {
			// Deduplicate: only forward the first header per stream.
			c.mu.Lock()
			dup := c.rxStreamActive && sid == c.rxStreamID
			if !dup {
				c.rxStreamID = sid
				c.rxStreamActive = true
				log.Printf("dcs: new RX stream %04X — forwarding header", sid)
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
			cs := dstar.PadCallsign(callsign, 8)
			clientMod := cs[7]
			if clientMod == ' ' {
				clientMod = 'A'
			}
			pkt := buildKeepalive(callsign, clientMod, refCall, refMod)
			conn.WriteToUDP(pkt, c.remoteAddr) //nolint:errcheck
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
