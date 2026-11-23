package dplus

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

// DPlus authentication registers a callsign and its public IP with the US
// Trust auth server, as DVTool, ircDDBGateway and hotspots do at startup, so
// Trust-checking gateways (Icom G2 + DPlus) can match the link to the callsign.
var (
	authAddr     = "auth.dstargateway.org:20001"
	authTimeout  = 10 * time.Second
	authInterval = 6 * time.Hour

	authMu   sync.Mutex
	authCall string
	authAt   time.Time
)

// baseCallsign strips the module letter from an 8-char "KR4GCQ D" style callsign.
func baseCallsign(call string) string {
	if len(call) == 8 && call[6] == ' ' && call[7] != ' ' {
		call = call[:7]
	}
	return strings.TrimSpace(call)
}

func buildAuthPacket(callsign string) []byte {
	pkt := []byte(strings.Repeat(" ", 56))
	pkt[0], pkt[1], pkt[2], pkt[3] = 0x38, 0xC0, 0x01, 0x00
	cs := callsign
	if len(cs) > 8 {
		cs = cs[:8]
	}
	copy(pkt[4:12], cs)
	copy(pkt[12:20], "DV019999")
	copy(pkt[28:32], "W7IB")
	pkt[32] = '2'
	copy(pkt[40:47], "DHS0257")
	return pkt
}

// authenticate logs callsign in to the auth server and returns how many active
// REF reflectors the reply listed. The server sends no explicit status.
func authenticate(callsign string) (int, error) {
	conn, err := net.DialTimeout("tcp", authAddr, authTimeout)
	if err != nil {
		return 0, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(authTimeout)) //nolint:errcheck
	if _, err := conn.Write(buildAuthPacket(callsign)); err != nil {
		return 0, err
	}

	refs := 0
	hdr := make([]byte, 2)
	for {
		if _, err := io.ReadFull(conn, hdr); err != nil {
			break // server closes when the list is done
		}
		n := int(hdr[1]&0x0F)<<8 | int(hdr[0])
		if hdr[1]&0xC0 != 0xC0 || n < 3 {
			return refs, fmt.Errorf("unexpected reply header % X", hdr)
		}
		body := make([]byte, n)
		copy(body, hdr)
		if _, err := io.ReadFull(conn, body[2:]); err != nil {
			break
		}
		if body[2] != 0x01 {
			return refs, fmt.Errorf("unexpected reply type 0x%02X", body[2])
		}
		for i := 8; i+26 <= n; i += 26 {
			name := strings.TrimSpace(string(body[i+16 : i+24]))
			if body[i+25]&0x80 != 0 && strings.HasPrefix(name, "REF") {
				refs++
			}
		}
	}
	return refs, nil
}

// ensureAuthenticated authenticates callsign unless it was done within
// authInterval. Failures are logged and not fatal: reflectors don't need it.
func ensureAuthenticated(callsign string) {
	call := baseCallsign(callsign)
	// REFCONNECT_DPLUS_AUTH=off keeps test runs off the real auth server.
	if call == "" || os.Getenv("REFCONNECT_DPLUS_AUTH") == "off" {
		return
	}
	authMu.Lock()
	defer authMu.Unlock()
	if call == authCall && time.Since(authAt) < authInterval {
		return
	}
	refs, err := authenticate(call)
	if err != nil {
		log.Printf("dplus: auth as %s with %s failed: %v (gateways may grant read-only links)", call, authAddr, err)
		return
	}
	log.Printf("dplus: authenticated %s with %s (%d REF reflectors listed)", call, authAddr, refs)
	authCall, authAt = call, time.Now()
}
