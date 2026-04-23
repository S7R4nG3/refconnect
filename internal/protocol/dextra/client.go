package dextra

import (
	"encoding/binary"
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

// dextraLogRXPackets controls verbose hex-dump logging of non-keepalive inbound
// packets. Disable for production to reduce log noise.
const dextraLogRXPackets = false

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

	stopCh         chan struct{}
	wg             sync.WaitGroup // tracks rxLoop + keepaliveLoop
	streamID       uint16
	rxStreamID     uint16 // current inbound stream ID for dedup
	rxStreamActive bool
}

// New returns a new DExtra client.  Call Connect to establish a link.
func New() *Client {
	return &Client{
		hdrCh:   make(chan dstar.DVHeader, 16),
		frmCh:   make(chan dstar.DVFrame, 64),
		eventCh: make(chan protocol.Event, 16),
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

	// Validate ACK: server echoes either 10 or 11 bytes of the connect packet then
	// appends "ACK\0".  Most XRF servers echo 10 bytes (dropping the trailing 0x00),
	// giving 14 bytes total with "ACK" at [10].  Some echo all 11 bytes, giving 15
	// bytes with "ACK" at [11].  Accept either.
	ackOK := (n >= 14 && buf[10] == 'A' && buf[11] == 'C' && buf[12] == 'K') ||
		(n >= 15 && buf[11] == 'A' && buf[12] == 'C' && buf[13] == 'K')
	if !ackOK {
		return fail(fmt.Sprintf("connect rejected by reflector (n=%d bytes: %X)", n, buf[:n]), fmt.Errorf("dextra: connect rejected"))
	}

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
	pkt := buildDisconnectPacket(c.cfg.MyCall)
	c.conn.WriteToUDP(pkt, c.remoteAddr) //nolint:errcheck
	close(c.stopCh)
	err := c.conn.Close()
	c.conn = nil
	c.setState(protocol.StateDisconnected, "Disconnected")
	// Wait for goroutines to exit before closing channels to avoid send-on-closed panic.
	c.wg.Wait()
	close(c.hdrCh)
	close(c.frmCh)
	close(c.eventCh)
	return err
}

func (c *Client) SendHeader(hdr dstar.DVHeader) error {
	c.mu.Lock()
	// Generate a new stream ID for each transmission so the reflector
	// treats it as a distinct voice stream.
	c.streamID = nextStreamID()
	conn, addr, sid := c.conn, c.remoteAddr, c.streamID
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dextra: not connected")
	}
	log.Printf("dextra: SendHeader streamID=%04X RPT1=%q RPT2=%q MYCALL=%q URCALL=%q",
		sid, hdr.RPT1, hdr.RPT2, hdr.MyCall, hdr.YourCall)
	pkt, err := encodeHeader(sid, hdr)
	if err != nil {
		return err
	}
	log.Printf("dextra: TX header packet (%d bytes):\n%s", len(pkt), hex.Dump(pkt))
	// Send header twice for UDP reliability.
	for i := 0; i < 2; i++ {
		if _, err = conn.WriteToUDP(pkt, addr); err != nil {
			return err
		}
	}
	return nil
}

func (c *Client) SendFrame(f dstar.DVFrame) error {
	c.mu.Lock()
	conn, addr, sid := c.conn, c.remoteAddr, c.streamID
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dextra: not connected")
	}
	pkt := encodeVoice(sid, f)
	_, err := conn.WriteToUDP(pkt, addr)
	return err
}

func (c *Client) RxHeaders() <-chan dstar.DVHeader { return c.hdrCh }
func (c *Client) RxFrames() <-chan dstar.DVFrame   { return c.frmCh }
func (c *Client) Events() <-chan protocol.Event    { return c.eventCh }

// rxLoop reads inbound UDP packets and dispatches them to hdrCh/frmCh.
func (c *Client) rxLoop() {
	defer c.wg.Done()
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return
	}
	buf := make([]byte, udpReadBuf)
	for {
		n, fromAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-c.stopCh:
			default:
				c.setState(protocol.StateError, "rx error: "+err.Error())
			}
			return
		}

		// Keepalive: 9 bytes ending in 0x00 — skip silently.
		if n == 9 && buf[8] == 0x00 {
			continue
		}
		if dextraLogRXPackets {
			show := min(n, 64)
			log.Printf("dextra: RX %d bytes from %s (byte[0]=0x%02X):\n%s",
				n, fromAddr, buf[0], hex.Dump(buf[:show]))
		}

		hdr, frm, err := parsePacket(buf[:n])
		if err != nil {
			log.Printf("dextra: parse error: %v", err)
			continue
		}
		if hdr != nil {
			// DExtra servers re-transmit the header packet periodically
			// throughout a transmission.  Only forward the FIRST header for
			// each new stream to avoid resetting the radio's voice decoder.
			var sid uint16
			if n >= 14 {
				sid = binary.LittleEndian.Uint16(buf[12:14])
			}
			c.mu.Lock()
			dup := c.rxStreamActive && sid == c.rxStreamID
			if !dup {
				c.rxStreamID = sid
				c.rxStreamActive = true
				log.Printf("dextra: new RX stream %04X — forwarding header", sid)
			}
			c.mu.Unlock()
			if dup {
				log.Printf("dextra: suppressing duplicate header for stream %04X", sid)
				continue
			}
			select {
			case c.hdrCh <- *hdr:
			default:
				log.Printf("dextra: dropped inbound header: channel full")
			}
		}
		if frm != nil {
			if frm.End {
				c.mu.Lock()
				c.rxStreamActive = false
				c.mu.Unlock()
				log.Printf("dextra: RX stream end")
			}
			select {
			case c.frmCh <- *frm:
			default:
			}
		}
	}
}

// keepaliveLoop sends a poll every keepaliveInterval to maintain the link.
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
			c.mu.Unlock()
			if conn == nil {
				return
			}
			pkt := buildKeepalive(callsign)
			conn.WriteToUDP(pkt, c.remoteAddr) //nolint:errcheck
		}
	}
}
