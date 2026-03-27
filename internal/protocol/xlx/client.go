package xlx

import (
	"fmt"
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

// Client implements protocol.Reflector for XLX reflectors using the native
// XLX control protocol on port 30001 with xlxd-compatible DSVT voice framing.
type Client struct {
	mu       sync.Mutex
	conn     *net.UDPConn
	cfg      protocol.Config
	state    atomic.Int32

	hdrCh   chan dstar.DVHeader
	frmCh   chan dstar.DVFrame
	eventCh chan protocol.Event

	stopCh   chan struct{}
	streamID uint16
}

// New returns a new XLX client.
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
		return fmt.Errorf("xlx: already connected or connecting")
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("xlx: resolve: %w", err)
	}
	conn, err := net.DialUDP("udp", nil, addr)
	if err != nil {
		return fmt.Errorf("xlx: dial: %w", err)
	}

	c.conn = conn
	c.cfg = cfg
	c.stopCh = make(chan struct{})
	c.streamID = nextStreamID()
	c.setState(protocol.StateConnecting, fmt.Sprintf("Connecting to XLX %s:%d module %c", cfg.Host, cfg.Port, cfg.Module))

	fail := func(msg string, err error) error {
		conn.Close()
		c.conn = nil
		c.setState(protocol.StateError, msg)
		return err
	}

	// Send 39-byte 'L' connect packet.
	pkt := buildConnectPacket(cfg.MyCall, cfg.Module)
	if _, err := conn.Write(pkt); err != nil {
		return fail("connect send: "+err.Error(), err)
	}

	// Wait for 39-byte 'A' ACK or 10-byte 'N' NACK.
	conn.SetReadDeadline(time.Now().Add(connectTimeout)) //nolint:errcheck
	buf := make([]byte, udpReadBuf)
	n, err := conn.Read(buf)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	if err != nil {
		return fail("connect timeout: "+err.Error(), fmt.Errorf("xlx: connect ack: %w", err))
	}

	if n >= 10 && buf[0] == 'N' && buf[9] == 0 {
		return fail("connect rejected (NACK) by reflector", fmt.Errorf("xlx: connect NACK"))
	}
	if n < 39 || buf[0] != 'A' || buf[38] != 0 {
		return fail(fmt.Sprintf("unexpected connect response (n=%d, buf[0]=%02X)", n, buf[0]), fmt.Errorf("xlx: connect rejected"))
	}

	c.setState(protocol.StateConnected, fmt.Sprintf("Linked to XLX %s module %c", cfg.Host, cfg.Module))
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
	c.conn.Write(buildDisconnectPacket(c.cfg.MyCall)) //nolint:errcheck
	close(c.stopCh)
	err := c.conn.Close()
	c.conn = nil
	c.setState(protocol.StateDisconnected, "Disconnected")
	return err
}

func (c *Client) SendHeader(hdr dstar.DVHeader) error {
	c.mu.Lock()
	conn := c.conn
	sid := c.streamID
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("xlx: not connected")
	}
	pkt, err := encodeHeader(sid, hdr)
	if err != nil {
		return err
	}
	_, err = conn.Write(pkt)
	return err
}

func (c *Client) SendFrame(f dstar.DVFrame) error {
	c.mu.Lock()
	conn := c.conn
	sid := c.streamID
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("xlx: not connected")
	}
	pkt := encodeVoice(sid, f)
	_, err := conn.Write(pkt)
	return err
}

func (c *Client) RxHeaders() <-chan dstar.DVHeader { return c.hdrCh }
func (c *Client) RxFrames() <-chan dstar.DVFrame   { return c.frmCh }
func (c *Client) Events() <-chan protocol.Event    { return c.eventCh }

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

		n, err := conn.Read(buf)
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
			conn.Write(buildKeepalive(callsign)) //nolint:errcheck
		}
	}
}
