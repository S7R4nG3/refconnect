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
	nickInUseDelay   = 30 * time.Second // wait for server to expire ghost nick
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

	serverMu   sync.Mutex
	serverNick string // ircDDB server bot nick (e.g. "s-grp1s1"), discovered from NAMES
}

// New creates a Client for the given 8-char D-STAR gateway callsign.
//
// ircDDB nick convention (observed from live #dstar channel):
//   <lowercase_base_callsign>-<module_number>
// where module letter maps to a 1-based number (A=1, B=2, … Z=26).
// Example: "KR4GCQ D" → base "kr4gcq", module 'D'=4 → nick "kr4gcq-4"
// A space suffix (bare callsign) defaults to module A → nick "kr4gcq-1".
func New(callsign string) *Client {
	// Pad to 8 chars first so the module letter is always at position 7,
	// even when the input has trailing spaces (e.g. "KR4GCQ  ").
	padded := dstar.PadCallsign(strings.ToUpper(callsign), 8)
	module := padded[7]
	base := strings.TrimRight(padded[:7], " ")
	if module < 'A' || module > 'Z' {
		module = 'A' // space or invalid → default to module A
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

// AnnounceUser sends an UPDATE PRIVMSG to the ircDDB server bot announcing
// a D-STAR transmission. The message is dropped silently if the client is
// not currently registered, no server bot has been discovered, or the send
// buffer is full.
//
// txText is the 20-char slow data text message (e.g. "RefConnect by KR4GCQ").
// Pass "" to omit the text field.
//
// ircDDB UPDATE format (sent to server bot, not #dstar):
//
//	PRIVMSG <server-nick> :UPDATE <date> <time> <mycall8> <rpt1-8> 0 <rpt2-8> <urcall8> <f1> <f2> <f3> <ext4> [00 <dest8> <tx_msg20>]
//
// Callsign spaces are replaced with '_' per ircDDB convention.
func (c *Client) AnnounceUser(hdr dstar.DVHeader, txText string) {
	if State(c.state.Load()) != StateRegistered {
		return
	}
	c.serverMu.Lock()
	bot := c.serverNick
	c.serverMu.Unlock()
	if bot == "" {
		return
	}

	now := time.Now().UTC()
	dateStr := now.Format("2006-01-02")
	timeStr := now.Format("15:04:05")

	myCall := ircSanitize(dstar.PadCallsign(strings.TrimSpace(hdr.MyCall), 8))
	rpt1 := ircSanitize(dstar.PadCallsign(strings.TrimSpace(hdr.RPT1), 8))
	rpt2 := ircSanitize(dstar.PadCallsign(strings.TrimSpace(hdr.RPT2), 8))
	urCall := ircSanitize(dstar.PadCallsign(strings.TrimSpace(hdr.YourCall), 8))
	ext := ircSanitize(dstar.PadCallsign(strings.TrimSpace(hdr.MyCallSuffix), 4))

	msg := fmt.Sprintf("PRIVMSG %s :UPDATE %s %s %s %s 0 %s %s %02X %02X %02X %s",
		bot, dateStr, timeStr,
		myCall, rpt1, rpt2, urCall,
		hdr.Flag1, hdr.Flag2, hdr.Flag3, ext,
	)

	if txText != "" {
		padded := dstar.PadCallsign(txText, 20)
		msg += fmt.Sprintf(" 00 %s %s", ircSanitize(rpt2), ircSanitizeText(padded))
	}

	select {
	case c.sendCh <- msg:
	default:
	}
}

// ircSanitize replaces characters outside [A-Z0-9/] with '_' for ircDDB fields.
func ircSanitize(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '/' {
			continue
		}
		b[i] = '_'
	}
	return string(b)
}

// ircSanitizeText replaces non-printable characters (outside ASCII 33-126) with '_'.
func ircSanitizeText(s string) string {
	b := []byte(s)
	for i, ch := range b {
		if ch > 32 && ch < 127 {
			continue
		}
		b[i] = '_'
	}
	return string(b)
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
	// Capture stopCh locally so Stop() setting c.stopCh = nil can't turn
	// our select cases into nil-channel receives that block forever.
	c.stopMu.Lock()
	stop := c.stopCh
	c.stopMu.Unlock()

	for {
		select {
		case <-stop:
			return
		default:
		}

		c.setState(StateConnecting, "Connecting to ircDDB…")
		addr := net.JoinHostPort(DefaultServer, fmt.Sprintf("%d", DefaultPort))
		conn, err := net.DialTimeout("tcp", addr, dialTimeout)
		if err != nil {
			c.setState(StateError, err.Error())
			select {
			case <-stop:
				return
			case <-time.After(reconnectDelay):
			}
			continue
		}

		delay := reconnectDelay
		if err := c.session(conn, stop); err != nil {
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
		case <-stop:
			return
		case <-time.After(delay):
		}
	}
}

// session runs one IRC session until the connection closes or Stop is called.
func (c *Client) session(conn net.Conn, stop <-chan struct{}) error {
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
		case <-stop:
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
			case "353": // RPL_NAMREPLY — scan for server bot nick (@s-...)
				for _, name := range parts[3:] {
					if strings.HasPrefix(name, "@s-") {
						bot := name[1:] // strip '@' prefix
						c.serverMu.Lock()
						c.serverNick = bot
						c.serverMu.Unlock()
						log.Printf("ircddb: discovered server bot: %s", bot)
					}
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
