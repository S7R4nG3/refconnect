// Package ircddb implements the ircDDB gateway registration client.
//
// ircDDB is an IRC-based distributed database used by D-STAR reflectors to
// look up gateway callsigns and their current IP addresses. By maintaining a
// persistent connection, the local callsign is automatically registered with
// the current public IP, allowing REF, XRF, and XLX reflectors to accept
// incoming link requests from this machine.
//
// Server: rr.openquad.net:9007 (round-robin across the global cluster)
package ircddb

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

const (
	DefaultServer    = "openquad.net"
	DefaultPort      = 9007
	gatewayChan      = "#dstar"
	pingInterval     = 60 * time.Second
	reconnectDelay   = 15 * time.Second
	nickInUseDelay   = 90 * time.Second // wait for server to expire ghost nick
	dialTimeout      = 10 * time.Second
)

// State represents the ircDDB registration state.
type State int32

const (
	StateDisconnected State = iota
	StateConnecting
	StateRegistered
	StateError
)

// errNickInUse is returned by session when the server rejects the nick with 433.
var errNickInUse = fmt.Errorf("nick already in use")

func (s State) String() string {
	switch s {
	case StateDisconnected:
		return "Disconnected"
	case StateConnecting:
		return "Connecting…"
	case StateRegistered:
		return "Registered"
	case StateError:
		return "Error"
	default:
		return "Unknown"
	}
}

// Event carries a state-change notification from the client to the UI.
type Event struct {
	State   State
	Message string
}

// Client maintains a persistent ircDDB connection, keeping the local
// callsign registered with the current public IP address.
type Client struct {
	nick         string // IRC nick: 8-char D-STAR callsign with spaces→underscores
	state        atomic.Int32
	eventCh      chan Event
	sendCh       chan string  // outbound IRC lines queued by AnnounceUser
	stopMu       sync.Mutex
	stopCh       chan struct{}
	registeredCh chan struct{} // closed once StateRegistered is first reached
}

// New creates a Client for the given 8-char D-STAR gateway callsign.
//
// ircDDB nick convention (observed from live #dstar channel):
//   <lowercase_base_callsign>-<module_number>
// where module letter maps to a 1-based number (A=1, B=2, … Z=26).
// Example: "KR4GCQ D" → base "kr4gcq", module 'D'=4 → nick "kr4gcq-4"
func New(callsign string) *Client {
	cs := strings.ToUpper(strings.TrimSpace(callsign))
	// Last character of the 8-char callsign is the module letter.
	// Characters before it (trimmed of trailing spaces) are the base callsign.
	module := byte('A')
	base := cs
	if len(cs) == 8 {
		module = cs[7]
		base = strings.TrimRight(cs[:7], " ")
	} else if len(cs) > 0 {
		module = cs[len(cs)-1]
		base = strings.TrimRight(cs[:len(cs)-1], " ")
	}
	if module < 'A' || module > 'Z' {
		module = 'A'
	}
	moduleNum := int(module-'A') + 1
	nick := fmt.Sprintf("%s-%d", strings.ToLower(base), moduleNum)
	return &Client{
		nick:         nick,
		eventCh:      make(chan Event, 8),
		sendCh:       make(chan string, 16),
		registeredCh: make(chan struct{}),
	}
}

// Start launches the background registration loop. Call once.
func (c *Client) Start() {
	c.stopMu.Lock()
	c.stopCh = make(chan struct{})
	c.stopMu.Unlock()
	go c.loop()
}

// Stop shuts down the background loop cleanly. Safe to call concurrently.
func (c *Client) Stop() {
	c.stopMu.Lock()
	defer c.stopMu.Unlock()
	if c.stopCh == nil {
		return
	}
	select {
	case <-c.stopCh:
	default:
		close(c.stopCh)
	}
	c.stopCh = nil
}

// State returns the current registration state.
func (c *Client) State() State {
	return State(c.state.Load())
}

// Events returns the channel of state-change notifications.
func (c *Client) Events() <-chan Event {
	return c.eventCh
}

// Nick returns the IRC nick being used for registration.
func (c *Client) Nick() string {
	return c.nick
}

// WaitRegistered blocks until the client reaches StateRegistered or the
// timeout expires. Returns true if registered within the timeout.
func (c *Client) WaitRegistered(timeout time.Duration) bool {
	select {
	case <-c.registeredCh:
		return true
	case <-time.After(timeout):
		return false
	}
}

// AnnounceUser sends a user-update PRIVMSG to #dstar announcing a D-STAR
// transmission. The message is dropped silently if the client is not currently
// registered or if the send buffer is full.
//
// ircDDB user-update format:
//
//	PRIVMSG #dstar :@U <mycall8> <rpt1-8> <rpt2-8> <urcall8>
func (c *Client) AnnounceUser(hdr dstar.DVHeader) {
	if State(c.state.Load()) != StateRegistered {
		return
	}
	msg := fmt.Sprintf("PRIVMSG %s :@U %s %s %s %s",
		gatewayChan,
		dstar.PadCallsign(strings.TrimSpace(hdr.MyCall), 8),
		dstar.PadCallsign(strings.TrimSpace(hdr.RPT1), 8),
		dstar.PadCallsign(strings.TrimSpace(hdr.RPT2), 8),
		dstar.PadCallsign(strings.TrimSpace(hdr.YourCall), 8),
	)
	select {
	case c.sendCh <- msg:
	default:
	}
}

func (c *Client) setState(s State, msg string) {
	c.state.Store(int32(s))
	if s == StateRegistered {
		select {
		case <-c.registeredCh: // already closed
		default:
			close(c.registeredCh)
		}
	}
	select {
	case c.eventCh <- Event{State: s, Message: msg}:
	default:
	}
}

// loop connects, runs a session, and reconnects on failure until Stop is called.
func (c *Client) loop() {
	for {
		select {
		case <-c.stopCh:
			return
		default:
		}

		c.setState(StateConnecting, "Connecting to ircDDB…")
		addr := net.JoinHostPort(DefaultServer, fmt.Sprintf("%d", DefaultPort))
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			c.setState(StateError, err.Error())
			select {
			case <-c.stopCh:
				return
			case <-time.After(reconnectDelay):
			}
			continue
		}

		delay := reconnectDelay
		if err := c.session(conn); err != nil {
			log.Printf("ircddb: session ended: %v", err)
			if err == errNickInUse {
				c.setState(StateConnecting, fmt.Sprintf("ircDDB nick in use — retrying in %s…", nickInUseDelay))
				delay = nickInUseDelay
			} else {
				c.setState(StateError, err.Error())
			}
		} else {
			c.setState(StateDisconnected, "ircDDB disconnected")
		}

		select {
		case <-c.stopCh:
			return
		case <-time.After(delay):
		}
	}
}

// session runs one IRC session until the connection closes or Stop is called.
func (c *Client) session(conn net.Conn) error {
	defer conn.Close()

	send := func(line string) error {
		log.Printf("ircddb: >> %s", line)
		_, err := fmt.Fprintf(conn, "%s\r\n", line)
		return err
	}

	// Standard IRC handshake.
	for _, cmd := range []string{
		"PASS NONE",
		fmt.Sprintf("NICK %s", c.nick),
		fmt.Sprintf("USER %s 0 * :%s", c.nick, c.nick),
	} {
		if err := send(cmd); err != nil {
			return err
		}
	}

	// Read lines from the server in a goroutine so we can also select on
	// the stop channel and the ping ticker.
	lines := make(chan string, 64)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			lines <- sc.Text()
		}
		if err := sc.Err(); err != nil {
			readErr <- err
		} else {
			readErr <- fmt.Errorf("connection closed by server")
		}
	}()

	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	nick := c.nick // local copy; may be updated on 433 (nick in use)
	joined := false

	for {
		select {
		case <-c.stopCh:
			send("QUIT :shutdown") //nolint:errcheck
			return nil

		case err := <-readErr:
			return err

		case line := <-lines:
			log.Printf("ircddb: << %s", line)
			parts := strings.Fields(line)
			if len(parts) == 0 {
				continue
			}

			// PING from server — must PONG or be disconnected.
			if parts[0] == "PING" {
				token := ""
				if len(parts) > 1 {
					token = parts[1]
				}
				if err := send("PONG " + token); err != nil {
					return err
				}
				continue
			}

			if len(parts) < 2 {
				continue
			}
			switch parts[1] {
			case "001": // RPL_WELCOME — now safe to join channels
				if err := send("JOIN " + gatewayChan); err != nil {
					return err
				}
			case "376", "422": // RPL_ENDOFMOTD / ERR_NOMOTD
				if !joined {
					joined = true
					c.setState(StateRegistered, fmt.Sprintf("ircDDB registered as %s", nick))
				}
			case "433": // ERR_NICKNAMEINUSE — ghost still alive; bail and wait
				send("QUIT :nick in use") //nolint:errcheck
				return errNickInUse
			case "ERROR":
				return fmt.Errorf("server error: %s", line)
			}

		case msg := <-c.sendCh:
			if err := send(msg); err != nil {
				return err
			}

		case <-ticker.C:
			if err := send("PING :keepalive"); err != nil {
				return err
			}
		}
	}
}
