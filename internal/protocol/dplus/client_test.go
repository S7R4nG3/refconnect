package dplus

import (
	"net"
	"testing"
	"time"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"github.com/S7R4nG3/refconnect/internal/protocol"
)

// fakeGateway echoes CT_LINK1 and answers CT_LINK2 with the given 4-byte status.
func fakeGateway(t *testing.T, status string) uint16 {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			switch n {
			case 5:
				conn.WriteToUDP(buf[:n], from) //nolint:errcheck
			case 28:
				conn.WriteToUDP(append([]byte{0x08, 0xC0, 0x04, 0x00}, status...), from) //nolint:errcheck
			}
		}
	}()
	return uint16(conn.LocalAddr().(*net.UDPAddr).Port)
}

func TestConnectLoginStatus(t *testing.T) {
	for _, tc := range []struct {
		status string
		ok     bool
	}{
		{"OKRW", true},
		{"OKRO", true},
		{"BUSY", false},
		{"FAIL", false},
	} {
		t.Run(tc.status, func(t *testing.T) {
			port := fakeGateway(t, tc.status)
			c := New()
			err := c.Connect(protocol.Config{Host: "127.0.0.1", Port: port, Module: 'B', MyCall: "N0CALL  "})
			if tc.ok {
				if err != nil {
					t.Fatalf("Connect: %v", err)
				}
				if c.State() != protocol.StateConnected {
					t.Fatalf("state = %v, want connected", c.State())
				}
				c.Disconnect() //nolint:errcheck
			} else if err == nil {
				c.Disconnect() //nolint:errcheck
				t.Fatal("Connect succeeded, want error")
			}
		})
	}
}

func TestCheckLinkTimeout(t *testing.T) {
	c := New()
	start := time.Now()
	c.lastRX = start
	c.readOnly = true
	c.checkLinkTimeout(start.Add(linkTimeout - time.Second))
	if c.linkLost {
		t.Fatal("link reported lost before timeout")
	}
	c.checkLinkTimeout(start.Add(linkTimeout))
	if !c.linkLost || c.State() != protocol.StateError {
		t.Fatalf("linkLost=%v state=%v, want lost/error", c.linkLost, c.State())
	}
	ev := <-c.Events()
	if ev.State != protocol.StateError {
		t.Fatalf("event state = %v", ev.State)
	}
	c.checkLinkTimeout(start.Add(2 * linkTimeout))
	select {
	case ev := <-c.Events():
		t.Fatalf("duplicate event: %+v", ev)
	default:
	}
}

func TestRXStreamIDIsPassedOn(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	sid := [2]byte{0x81, 0x39}
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			switch n {
			case 5:
				conn.WriteToUDP(buf[:n], from) //nolint:errcheck
			case 28:
				conn.WriteToUDP([]byte{0x08, 0xC0, 0x04, 0x00, 'O', 'K', 'R', 'W'}, from) //nolint:errcheck
				hdr, _ := encodeHeader(sid, dstar.DVHeader{MyCall: "M7GSV   ", YourCall: "CQCQCQ  ", RPT1: "M7GSV  B", RPT2: "W4FJ   B"})
				conn.WriteToUDP(hdr, from)                                     //nolint:errcheck
				conn.WriteToUDP(encodeVoice(sid, dstar.DVFrame{Seq: 3}), from) //nolint:errcheck
			}
		}
	}()

	c := New()
	port := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
	if err := c.Connect(protocol.Config{Host: "127.0.0.1", Port: port, Module: 'B', MyCall: "N0CALL  "}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer c.Disconnect() //nolint:errcheck

	const want = 0x8139
	select {
	case h := <-c.RxHeaders():
		if h.StreamID != want {
			t.Fatalf("header StreamID = %04X, want %04X", h.StreamID, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no header")
	}
	select {
	case f := <-c.RxFrames():
		if f.StreamID != want {
			t.Fatalf("frame StreamID = %04X, want %04X", f.StreamID, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no frame")
	}
}

// A gateway that still has us linked streams its traffic ahead of the login
// ACK (seen on W4FJ); the voice packets must not be read as the ACK.
func TestConnectSkipsTrafficBeforeLoginAck(t *testing.T) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 64)
		voice := encodeVoice([2]byte{0xFD, 0x6A}, dstar.DVFrame{Seq: 5})
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			switch n {
			case 5:
				conn.WriteToUDP(buf[:n], from) //nolint:errcheck
				conn.WriteToUDP(voice, from)   //nolint:errcheck
			case 28:
				conn.WriteToUDP(voice, from)                                              //nolint:errcheck
				conn.WriteToUDP(voice, from)                                              //nolint:errcheck
				conn.WriteToUDP([]byte{0x08, 0xC0, 0x04, 0x00, 'O', 'K', 'R', 'W'}, from) //nolint:errcheck
			}
		}
	}()

	c := New()
	port := uint16(conn.LocalAddr().(*net.UDPAddr).Port)
	if err := c.Connect(protocol.Config{Host: "127.0.0.1", Port: port, Module: 'B', MyCall: "N0CALL  "}); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	c.Disconnect() //nolint:errcheck
}
