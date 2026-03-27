package dplus

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

	stopCh   chan struct{}
	streamID [4]byte
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

	// Step 1: send CT_LINK1 and wait for server echo.
	link1 := buildLink1Packet(true)
	log.Printf("dplus: sending CT_LINK1 to %s:%d:\n%s", cfg.Host, cfg.Port, hex.Dump(link1))
	if _, err := conn.WriteToUDP(link1, addr); err != nil {
		return fail("CT_LINK1 send: "+err.Error(), err)
	}

	conn.SetReadDeadline(time.Now().Add(connectTimeout)) //nolint:errcheck
	buf := make([]byte, udpReadBuf)
	n, fromAddr, err := conn.ReadFromUDP(buf)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	if err != nil {
		return fail("CT_LINK1 echo timeout: "+err.Error(), fmt.Errorf("dplus: CT_LINK1 echo: %w", err))
	}
	log.Printf("dplus: CT_LINK1 echo: %d bytes from %s:\n%s", n, fromAddr, hex.Dump(buf[:n]))

	// Step 2: send CT_LINK2 login packet and wait for OKRW ACK.
	loginPkt := buildLoginPacket(cfg.MyCall)
	log.Printf("dplus: sending CT_LINK2 login to %s:%d:\n%s", cfg.Host, cfg.Port, hex.Dump(loginPkt))
	if _, err := conn.WriteToUDP(loginPkt, addr); err != nil {
		return fail("CT_LINK2 send: "+err.Error(), err)
	}

	conn.SetReadDeadline(time.Now().Add(connectTimeout)) //nolint:errcheck
	n, fromAddr, err = conn.ReadFromUDP(buf)
	conn.SetReadDeadline(time.Time{}) //nolint:errcheck
	if err != nil {
		return fail("login ack timeout: "+err.Error(), fmt.Errorf("dplus: login ack: %w", err))
	}
	log.Printf("dplus: login ack: %d bytes from %s:\n%s", n, fromAddr, hex.Dump(buf[:n]))

	// ACK is 8 bytes: 08 C0 04 00 4F 4B 52 57 ("OKRW" at bytes [4-7]).
	if n < 8 || buf[4] != 'O' || buf[5] != 'K' || buf[6] != 'R' || buf[7] != 'W' {
		show := n
		if show > 8 {
			show = 8
		}
		return fail(fmt.Sprintf("login rejected (expected OKRW, got %q)", buf[:show]), fmt.Errorf("dplus: login rejected"))
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
	c.conn.WriteToUDP(buildDisconnectPacket(), c.remoteAddr) //nolint:errcheck
	close(c.stopCh)
	err := c.conn.Close()
	c.conn = nil
	c.setState(protocol.StateDisconnected, "Disconnected")
	return err
}

func (c *Client) SendHeader(hdr dstar.DVHeader) error {
	c.mu.Lock()
	conn, addr, sid := c.conn, c.remoteAddr, c.streamID
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dplus: not connected")
	}
	pkt, err := encodeHeader(sid, hdr)
	if err != nil {
		return err
	}
	_, err = conn.WriteToUDP(pkt, addr)
	return err
}

func (c *Client) SendFrame(f dstar.DVFrame) error {
	c.mu.Lock()
	conn, addr, sid := c.conn, c.remoteAddr, c.streamID
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("dplus: not connected")
	}
	pkt := encodeVoice(sid, f)
	_, err := conn.WriteToUDP(pkt, addr)
	return err
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
		pktLen := int(binary.LittleEndian.Uint16(buf[0:2]))
		if pktLen < 2 || pktLen > n {
			continue
		}
		payload := buf[2:pktLen]

		hdr, frm, err := parsePacket(payload)
		if err != nil {
			continue
		}
		if hdr != nil {
			select {
			case c.hdrCh <- *hdr:
			default:
				log.Printf("dplus: dropped inbound header: channel full")
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
			c.mu.Unlock()
			if conn == nil {
				return
			}
			conn.WriteToUDP(buildKeepalive(), c.remoteAddr) //nolint:errcheck
		}
	}
}
