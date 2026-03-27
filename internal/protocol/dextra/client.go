package dextra

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
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

// Client implements protocol.Reflector for the DExtra protocol.
type Client struct {
	mu         sync.Mutex
	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	cfg        protocol.Config
	state      atomic.Int32 // stores protocol.LinkState

	hdrCh   chan dstar.DVHeader
	frmCh   chan dstar.DVFrame
	eventCh chan protocol.Event

	stopCh   chan struct{}
	streamID [4]byte
}

// New returns a new DExtra client.  Call Connect to establish a link.
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
		return fmt.Errorf("dextra: already connected or connecting")
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("dextra: resolve %s:%d: %w", cfg.Host, cfg.Port, err)
	}
	// Use an unconnected socket so responses arriving from any source address
	// (e.g. a different node in a reflector cluster) are still received.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{})
	if err != nil {
		return fmt.Errorf("dextra: listen: %w", err)
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

	// Send the connect packet.
	pkt := buildConnectPacket(cfg.MyCall, cfg.Module)
	log.Printf("dextra: sending connect packet to %s:%d:\n%s", cfg.Host, cfg.Port, hex.Dump(pkt))
	if _, err := conn.WriteToUDP(pkt, addr); err != nil {
		return fail("send connect: "+err.Error(), err)
	}

	// Wait for ACK with a deadline.
	conn.SetReadDeadline(time.Now().Add(connectTimeout)) //nolint:errcheck
	buf := make([]byte, udpReadBuf)
	n, fromAddr, err := conn.ReadFromUDP(buf)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	if err != nil {
		return fail("connect timeout: "+err.Error(), fmt.Errorf("dextra: connect ack: %w", err))
	}
	log.Printf("dextra: received %d bytes from %s:\n%s", n, fromAddr, hex.Dump(buf[:n]))

	// Validate ACK: server echoes the connect packet (10 bytes) + "ACK\0" = 14 bytes.
	// bytes [10-12] must be 'A', 'C', 'K'.
	if n < 14 || buf[10] != 'A' || buf[11] != 'C' || buf[12] != 'K' {
		return fail(fmt.Sprintf("connect rejected by reflector (n=%d)", n), fmt.Errorf("dextra: connect rejected"))
	}

	c.setState(protocol.StateConnected, fmt.Sprintf("Linked to %s module %c", cfg.Host, cfg.Module))
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
	pkt := buildDisconnectPacket(c.cfg.MyCall)
	c.conn.WriteToUDP(pkt, c.remoteAddr) //nolint:errcheck
	close(c.stopCh)
	err := c.conn.Close()
	c.conn = nil
	c.setState(protocol.StateDisconnected, "Disconnected")
	return err
}

func (c *Client) SendHeader(hdr dstar.DVHeader) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dextra: not connected")
	}
	pkt, err := encodeHeader(c.streamID, hdr)
	if err != nil {
		return err
	}
	c.mu.Lock()
	addr := c.remoteAddr
	c.mu.Unlock()
	_, err = conn.WriteToUDP(pkt, addr)
	return err
}

func (c *Client) SendFrame(f dstar.DVFrame) error {
	c.mu.Lock()
	conn := c.conn
	addr := c.remoteAddr
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dextra: not connected")
	}
	pkt := encodeVoice(c.streamID, f)
	_, err := conn.WriteToUDP(pkt, addr)
	return err
}

func (c *Client) RxHeaders() <-chan dstar.DVHeader { return c.hdrCh }
func (c *Client) RxFrames() <-chan dstar.DVFrame   { return c.frmCh }
func (c *Client) Events() <-chan protocol.Event    { return c.eventCh }

// rxLoop reads inbound UDP packets and dispatches them to hdrCh/frmCh.
func (c *Client) rxLoop() {
	buf := make([]byte, udpReadBuf)
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}
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

		hdr, frm, err := parsePacket(buf[:n])
		if err != nil {
			continue
		}
		if hdr != nil {
			select {
			case c.hdrCh <- *hdr:
			default:
			}
		}
		if frm != nil {
			select {
			case c.frmCh <- *frm:
			default:
			}
		}
	}
}

// keepaliveLoop sends a poll every keepaliveInterval to maintain the link.
func (c *Client) keepaliveLoop() {
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
			c.mu.Unlock()
			if conn == nil {
				return
			}
			pkt := buildKeepalive(callsign)
			conn.WriteToUDP(pkt, c.remoteAddr) //nolint:errcheck
		}
	}
}
