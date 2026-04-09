package dplus

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

// Client implements protocol.Reflector for DPlus (REF reflectors) over UDP port 20001.
type Client struct {
	mu         sync.Mutex
	conn       *net.UDPConn
	remoteAddr *net.UDPAddr
	cfg        protocol.Config
	state      atomic.Int32

	hdrCh   chan dstar.DVHeader
	frmCh   chan dstar.DVFrame
	eventCh chan protocol.Event

	stopCh           chan struct{}
	streamID         [2]byte
	rxKeepaliveCount int
	rxStreamID       [2]byte // current inbound stream ID for dedup
	rxStreamActive   bool
}

// New returns a new DPlus client.
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
		return fmt.Errorf("dplus: already connected or connecting")
	}

	addr, err := net.ResolveUDPAddr("udp", fmt.Sprintf("%s:%d", cfg.Host, cfg.Port))
	if err != nil {
		return fmt.Errorf("dplus: resolve: %w", err)
	}
	// Use an unconnected socket so responses from any source address are received.
	// REF reflectors expect packets to originate from port 20001; try binding
	// locally first, fall back to ephemeral if 20001 is already in use.
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{Port: DefaultPort})
	if err != nil {
		log.Printf("dplus: could not bind local port %d (%v), using ephemeral port", DefaultPort, err)
		conn, err = net.ListenUDP("udp4", &net.UDPAddr{})
		if err != nil {
			return fmt.Errorf("dplus: listen: %w", err)
		}
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

	buf := make([]byte, udpReadBuf)

	// readControl reads UDP packets, skipping 3-byte keepalives, until it
	// receives a packet of at least minLen bytes or the deadline expires.
	readControl := func(minLen int, desc string) (int, error) {
		deadline := time.Now().Add(connectTimeout)
		for {
			conn.SetReadDeadline(deadline) //nolint:errcheck
			n, from, err := conn.ReadFromUDP(buf)
			conn.SetReadDeadline(time.Time{}) //nolint:errcheck
			if err != nil {
				return 0, fmt.Errorf("dplus: %s: %w", desc, err)
			}
			if n == 3 && buf[0] == 0x03 && buf[1] == 0x60 {
				log.Printf("dplus: skipping keepalive while waiting for %s", desc)
				continue
			}
			log.Printf("dplus: %s: %d bytes from %s:\n%s", desc, n, from, hex.Dump(buf[:n]))
			if n < minLen {
				log.Printf("dplus: %s too short (%d bytes), skipping", desc, n)
				continue
			}
			return n, nil
		}
	}

	// Step 1: send CT_LINK1 and wait for server echo.
	link1 := buildLink1Packet(true)
	log.Printf("dplus: sending CT_LINK1 to %s:%d:\n%s", cfg.Host, cfg.Port, hex.Dump(link1))
	if _, err := conn.WriteToUDP(link1, addr); err != nil {
		return fail("CT_LINK1 send: "+err.Error(), err)
	}
	if _, err := readControl(5, "CT_LINK1 echo"); err != nil {
		return fail("CT_LINK1 echo timeout: "+err.Error(), err)
	}

	// Step 2: send CT_LINK2 login packet and wait for OKRW or BUSY ACK.
	loginPkt := buildLoginPacket(cfg.MyCall)
	log.Printf("dplus: sending CT_LINK2 login to %s:%d:\n%s", cfg.Host, cfg.Port, hex.Dump(loginPkt))
	if _, err := conn.WriteToUDP(loginPkt, addr); err != nil {
		return fail("CT_LINK2 send: "+err.Error(), err)
	}
	n, err := readControl(8, "login ack")
	if err != nil {
		return fail("login ack timeout: "+err.Error(), err)
	}

	// ACK is 8 bytes: 08 C0 04 00 + 4-byte status.
	// "OKRW" (4F 4B 52 57) = accepted; "BUSY" (42 55 53 59) = module in use.
	if buf[4] == 'B' && buf[5] == 'U' && buf[6] == 'S' && buf[7] == 'Y' {
		return fail("module busy — reflector already linked; wait a moment and retry",
			fmt.Errorf("dplus: module busy"))
	}
	if n < 8 || buf[4] != 'O' || buf[5] != 'K' || buf[6] != 'R' || buf[7] != 'W' {
		show := n
		if show > 8 {
			show = 8
		}
		return fail(fmt.Sprintf("login rejected (expected OKRW, got %q)", buf[:show]),
			fmt.Errorf("dplus: login rejected"))
	}

	log.Printf("dplus: connected from local %s to %s", conn.LocalAddr(), addr)
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
	disc := buildDisconnectPacket()
	c.conn.WriteToUDP(disc, c.remoteAddr) //nolint:errcheck
	c.conn.WriteToUDP(disc, c.remoteAddr) //nolint:errcheck
	close(c.stopCh)
	err := c.conn.Close()
	c.conn = nil
	c.setState(protocol.StateDisconnected, "Disconnected")
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
		return fmt.Errorf("dplus: not connected")
	}
	log.Printf("dplus: SendHeader streamID=%02X RPT1=%q RPT2=%q MYCALL=%q URCALL=%q",
		sid, hdr.RPT1, hdr.RPT2, hdr.MyCall, hdr.YourCall)
	pkt, err := encodeHeader(sid, hdr)
	if err != nil {
		return err
	}
	log.Printf("dplus: TX header packet (%d bytes):\n%s", len(pkt), hex.Dump(pkt))
	// Send the header twice for UDP reliability, matching what reflectors do
	// on the RX path. Some reflector implementations require at least two
	// header copies before they start forwarding voice frames.
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
		return fmt.Errorf("dplus: not connected")
	}
	pkt := encodeVoice(sid, f)
	n, err := conn.WriteToUDP(pkt, addr)
	if err != nil {
		log.Printf("dplus: TX voice write error: %v", err)
		return err
	}
	if n != len(pkt) {
		log.Printf("dplus: TX voice short write: %d/%d bytes", n, len(pkt))
	}
	return nil
}

func (c *Client) RxHeaders() <-chan dstar.DVHeader { return c.hdrCh }
func (c *Client) RxFrames() <-chan dstar.DVFrame   { return c.frmCh }
func (c *Client) Events() <-chan protocol.Event    { return c.eventCh }

// rxLoop reads inbound DPlus UDP packets.
func (c *Client) rxLoop() {
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
		if n < 2 {
			continue
		}
		// Count and occasionally log keepalives; log everything else.
		if n == 3 && buf[0] == 0x03 && buf[1] == 0x60 {
			c.mu.Lock()
			c.rxKeepaliveCount++
			cnt := c.rxKeepaliveCount
			c.mu.Unlock()
			if cnt == 1 || cnt%20 == 0 {
				log.Printf("dplus: RX keepalive #%d", cnt)
			}
			continue
		}
		// Log ALL non-keepalive inbound packets for debugging.
		show := n
		if show > 64 {
			show = 64
		}
		log.Printf("dplus: RX packet (%d bytes, byte[0]=0x%02X byte[1]=0x%02X):\n%s",
			n, buf[0], buf[1], hex.Dump(buf[:show]))

		// Parse DSVT data packets (byte[1] = 0x80).
		hdr, frm, err := parsePacket(buf[:n])
		if err != nil {
			log.Printf("dplus: parse error: %v", err)
			continue
		}
		if hdr != nil {
			// Extract stream ID (bytes 14-15) for deduplication.
			// The reflector re-sends headers periodically (~every 420ms)
			// throughout a transmission. Only forward the FIRST header per
			// stream to avoid resetting the radio's voice decoder.
			var sid [2]byte
			if n >= 16 {
				copy(sid[:], buf[14:16])
			}
			c.mu.Lock()
			dup := c.rxStreamActive && sid == c.rxStreamID
			if !dup {
				c.rxStreamID = sid
				c.rxStreamActive = true
				log.Printf("dplus: new RX stream %02X — forwarding header", sid)
			}
			c.mu.Unlock()
			if dup {
				continue // suppress duplicate header
			}
			select {
			case c.hdrCh <- *hdr:
			default:
				log.Printf("dplus: dropped inbound header: channel full")
			}
		}
		if frm != nil {
			if frm.End {
				c.mu.Lock()
				c.rxStreamActive = false
				c.mu.Unlock()
			}
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
			c.mu.Unlock()
			if conn == nil {
				return
			}
			conn.WriteToUDP(buildKeepalive(), c.remoteAddr) //nolint:errcheck
		}
	}
}
