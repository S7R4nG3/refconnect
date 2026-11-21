package dcs

import (
	"net"
	"testing"

	"github.com/S7R4nG3/refconnect/internal/protocol"
)

// fakeServer answers the first connect packet with reply.
func fakeServer(t *testing.T, reply []byte) uint16 {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buf := make([]byte, 1024)
		n, from, err := conn.ReadFromUDP(buf)
		if err != nil || n != connectPacketLen {
			return
		}
		conn.WriteToUDP(reply, from) //nolint:errcheck
	}()
	return uint16(conn.LocalAddr().(*net.UDPAddr).Port)
}

// Repeater gateways reply with a 14-byte ACK/NAK, not the 22-byte reflector reply.
func TestConnectRepeaterGatewayReply(t *testing.T) {
	for _, c := range []struct {
		reply string
		ok    bool
	}{
		{"N0TEST  GBACK\x00", true},
		{"N0TEST  GBNAK\x00", false},
	} {
		c := c
		t.Run(c.reply[10:13], func(t *testing.T) {
			port := fakeServer(t, []byte(c.reply))
			cl := New()
			err := cl.Connect(protocol.Config{Host: "127.0.0.1", Port: port, Module: 'B',
				MyCall: "N0TEST", TargetCall: "N0CALL"})
			if c.ok {
				if err != nil {
					t.Fatalf("Connect: %v", err)
				}
				defer cl.Disconnect() //nolint:errcheck
				if cl.State() != protocol.StateConnected {
					t.Fatalf("state = %v, want connected", cl.State())
				}
			} else if err == nil {
				cl.Disconnect() //nolint:errcheck
				t.Fatal("Connect succeeded on a NAK")
			}
		})
	}
}
