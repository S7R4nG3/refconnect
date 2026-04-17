// APRS-IS client for forwarding position reports to the APRS Internet Service.
//
// APRS-IS is a TCP-based network that distributes APRS packets worldwide.
// Clients connect to a server (e.g. rotate.aprs2.net:14580), authenticate
// with a callsign and passcode, and then send TNC2-format packets.
//
// The passcode is derived from the base callsign using a well-known XOR
// hash algorithm documented in the APRS-IS specification.
package aprs

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultAPRSISServer is a round-robin DNS entry for the Tier 2 APRS-IS
	// network, which load-balances across multiple regional servers.
	DefaultAPRSISServer = "rotate.aprs2.net:14580"

	aprsISDialTimeout  = 10 * time.Second
	aprsISWriteTimeout = 10 * time.Second
)

// APRSISClient maintains a connection to an APRS-IS server and sends
// position reports. It is safe for concurrent use.
type APRSISClient struct {
	server   string
	callsign string // base callsign (no SSID)
	passcode string
	swName   string
	swVer    string

	mu   sync.Mutex
	conn net.Conn
}

// NewAPRSISClient creates an APRS-IS client. callsign is the base callsign
// (e.g. "KR4GCQ"), swName/swVer identify the software in the login line.
func NewAPRSISClient(callsign, swName, swVer string) *APRSISClient {
	call := strings.ToUpper(strings.TrimSpace(callsign))
	return &APRSISClient{
		server:   DefaultAPRSISServer,
		callsign: call,
		passcode: fmt.Sprintf("%d", aprsPasscode(call)),
		swName:   swName,
		swVer:    swVer,
	}
}

// Send connects (if needed), authenticates, and transmits a TNC2-format
// APRS packet to APRS-IS. The packet should be the full TNC2 line
// (e.g. "KR4GCQ-1>APDPRS,DSTAR*:!3340.00N/08425.00W>RefConnect").
func (c *APRSISClient) Send(packet string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn == nil {
		if err := c.connect(); err != nil {
			return err
		}
	}

	// Write the packet with a trailing newline.
	c.conn.SetWriteDeadline(time.Now().Add(aprsISWriteTimeout)) //nolint:errcheck
	_, err := fmt.Fprintf(c.conn, "%s\r\n", packet)
	if err != nil {
		// Connection went stale; drop it so next Send reconnects.
		c.conn.Close()
		c.conn = nil
		return fmt.Errorf("aprs-is: write failed: %w", err)
	}
	log.Printf("aprs-is: sent %s", packet)
	return nil
}

// Close shuts down the APRS-IS connection.
func (c *APRSISClient) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		c.conn.Close()
		c.conn = nil
	}
}

// connect dials the APRS-IS server and sends the login line. Must be
// called with c.mu held.
func (c *APRSISClient) connect() error {
	conn, err := net.DialTimeout("tcp", c.server, aprsISDialTimeout)
	if err != nil {
		return fmt.Errorf("aprs-is: dial %s: %w", c.server, err)
	}

	// Read the server greeting (starts with "# ").
	conn.SetReadDeadline(time.Now().Add(aprsISDialTimeout)) //nolint:errcheck
	reader := bufio.NewReader(conn)
	greeting, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("aprs-is: read greeting: %w", err)
	}
	log.Printf("aprs-is: server: %s", strings.TrimSpace(greeting))

	// Send login.
	login := fmt.Sprintf("user %s pass %s vers %s %s\r\n",
		c.callsign, c.passcode, c.swName, c.swVer)
	conn.SetWriteDeadline(time.Now().Add(aprsISWriteTimeout)) //nolint:errcheck
	if _, err := conn.Write([]byte(login)); err != nil {
		conn.Close()
		return fmt.Errorf("aprs-is: login write: %w", err)
	}

	// Read the login response (should contain "verified" or "unverified").
	conn.SetReadDeadline(time.Now().Add(aprsISDialTimeout)) //nolint:errcheck
	response, err := reader.ReadString('\n')
	if err != nil {
		conn.Close()
		return fmt.Errorf("aprs-is: read login response: %w", err)
	}
	respStr := strings.TrimSpace(response)
	log.Printf("aprs-is: login response: %s", respStr)

	// If the server says "unverified", our passcode was wrong. The server
	// accepts the connection but silently drops all packets we send.
	if strings.Contains(strings.ToLower(respStr), "unverified") {
		conn.Close()
		return fmt.Errorf("aprs-is: login UNVERIFIED for %s — check callsign (passcode=%s)", c.callsign, c.passcode)
	}

	// Clear deadlines for subsequent writes.
	conn.SetReadDeadline(time.Time{})  //nolint:errcheck
	conn.SetWriteDeadline(time.Time{}) //nolint:errcheck

	c.conn = conn
	log.Printf("aprs-is: connected and verified on %s as %s", c.server, c.callsign)
	return nil
}

// aprsPasscode computes the APRS-IS authentication passcode for a callsign.
// This is the standard algorithm documented in the APRS-IS specification:
// a 15-bit XOR hash of the base callsign (without SSID), processed in
// pairs of bytes.
func aprsPasscode(callsign string) int16 {
	// Strip SSID if present.
	call := callsign
	if idx := strings.IndexByte(call, '-'); idx >= 0 {
		call = call[:idx]
	}
	call = strings.ToUpper(call)

	hash := int16(0x73E2)
	for i := 0; i+1 < len(call); i += 2 {
		hash ^= int16(call[i]) << 8
		hash ^= int16(call[i+1])
	}
	// If odd number of characters, XOR the last one shifted.
	if len(call)%2 == 1 {
		hash ^= int16(call[len(call)-1]) << 8
	}
	return hash & 0x7FFF
}
