//go:build gateway

package router

// Integration test against DStarGateway; run it with testing/gateway/run.sh.
// The gateway serves repeater N0CALL B and sends what it would transmit to
// UDP 20011 as homebrew (DSRP) packets.

import (
	"bytes"
	"net"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"github.com/S7R4nG3/refconnect/internal/protocol"
	"github.com/S7R4nG3/refconnect/internal/protocol/dcs"
	"github.com/S7R4nG3/refconnect/internal/protocol/dextra"
	"github.com/S7R4nG3/refconnect/internal/protocol/dplus"
)

const (
	gwRepeater = "N0CALL"
	gwModule   = 'B'
	gwMyCall   = "N0TEST D"
	gwRFPort   = 20011 // where the gateway sends the repeater's RF traffic
)

// gwHeard is what the gateway forwarded to the repeater for one transmission.
type gwHeard struct {
	header  []byte           // DSRP 0x20 packet, nil if none arrived
	frames  int              // DSRP 0x21 packets
	headers int              // DSRP 0x20 packets (an echo reply is a second one)
	linkRX  []dstar.DVHeader // headers the gateway sent back over the link
}

func gatewayHost() string {
	if h := os.Getenv("REFCONNECT_GATEWAY_HOST"); h != "" {
		return h
	}
	return "127.0.0.1"
}

// gwTransmit links, transmits through the router and returns what reached the repeater.
func gwTransmit(t *testing.T, rf *net.UDPConn, ref protocol.Reflector, port uint16, cfg Config) gwHeard {
	return gwTransmitUR(t, rf, ref, port, cfg, dstar.CQCall, 2*time.Second)
}

// gwTransmitUR transmits with the given URCALL and listens for wait after the last frame.
func gwTransmitUR(t *testing.T, rf *net.UDPConn, ref protocol.Reflector, port uint16, cfg Config,
	urcall string, wait time.Duration) gwHeard {
	t.Helper()
	err := ref.Connect(protocol.Config{
		Host: gatewayHost(), Port: port, Module: gwModule, MyCall: gwMyCall,
		Protocol: cfg.Protocol, TargetCall: gwRepeater,
	})
	if err != nil {
		t.Fatalf("link to gateway: %v", err)
	}
	defer ref.Disconnect() //nolint:errcheck
	go func() {
		for range ref.Events() {
		}
	}()

	rad := newFakeRadio()
	cfg.MyCall = gwMyCall
	rt := New(rad, ref, cfg)
	rt.Start()
	defer rt.Stop()
	var linkMu sync.Mutex
	var linkRX []dstar.DVHeader
	go func() {
		for e := range rt.Events() {
			if e.Direction == DirRX && e.Header != nil {
				linkMu.Lock()
				linkRX = append(linkRX, *e.Header)
				linkMu.Unlock()
			}
		}
	}()

	// Clear anything left over from an earlier transmission.
	buf := make([]byte, 64)
	for {
		rf.SetReadDeadline(time.Now().Add(200 * time.Millisecond)) //nolint:errcheck
		if _, err := rf.Read(buf); err != nil {
			break
		}
	}

	const nFrames = 2 * (dstar.MaxSeq + 1) // about 0.8 s
	go func() {
		rad.hdrCh <- dstar.DVHeader{MyCall: "N0TEST  ", MyCallSuffix: "TEST", YourCall: urcall,
			RPT1: "DIRECT  ", RPT2: "DIRECT  "}
		for i := 0; i < nFrames; i++ {
			seq := uint8(i % (dstar.MaxSeq + 1))
			f := dstar.DVFrame{Seq: seq, AMBE: dstar.SilenceAMBE, End: i == nFrames-1}
			if seq == 0 {
				f.SlowData = dstar.SyncSlowData
			}
			rad.frmCh <- f
			time.Sleep(20 * time.Millisecond)
		}
	}()

	var heard gwHeard
	deadline := time.Now().Add(nFrames*20*time.Millisecond + wait)
	for {
		rf.SetReadDeadline(deadline) //nolint:errcheck
		n, err := rf.Read(buf)
		if err != nil {
			linkMu.Lock()
			heard.linkRX = append(heard.linkRX, linkRX...)
			linkMu.Unlock()
			return heard
		}
		if n < 5 || !bytes.Equal(buf[:4], []byte("DSRP")) {
			continue
		}
		switch buf[4] {
		case 0x20:
			heard.headers++
			if heard.header == nil {
				heard.header = append([]byte(nil), buf[:n]...)
			}
		case 0x21:
			heard.frames++
		}
	}
}

func TestGatewayRepeaterLink(t *testing.T) {
	rf, err := net.ListenUDP("udp4", &net.UDPAddr{Port: gwRFPort})
	if err != nil {
		t.Fatalf("listen for the gateway's repeater traffic: %v", err)
	}
	defer rf.Close()

	for _, c := range []struct {
		proto protocol.Protocol
		port  uint16
		ref   func() protocol.Reflector
	}{
		{protocol.ProtoDPlus, dplus.DefaultPort, func() protocol.Reflector { return dplus.New() }},
		{protocol.ProtoDExtra, 30001, func() protocol.Reflector { return dextra.New() }},
		{protocol.ProtoDCS, dcs.DefaultPort, func() protocol.Reflector { return dcs.New() }},
	} {
		t.Run(string(c.proto), func(t *testing.T) {
			heard := gwTransmit(t, rf, c.ref(), c.port, Config{
				ReflectorCall: gwRepeater, ReflectorModule: gwModule, Repeater: true, Protocol: c.proto,
			})
			if heard.header == nil {
				t.Fatal("gateway put no header on the air")
			}
			// DSRP header: RPT2 [11:19], RPT1 [19:27], UR [27:35], MY [35:43].
			h := heard.header
			t.Logf("on air: RPT1=%q RPT2=%q UR=%q MY=%q, %d frames", h[19:27], h[11:19], h[27:35], h[35:43], heard.frames)
			if got := string(h[35:43]); got != "N0TEST  " {
				t.Errorf("MYCALL on air = %q", got)
			}
			// On air RPT1 is the gateway and RPT2 the module.
			if got := string(h[11:19]); got != "N0CALL B" {
				t.Errorf("RPT2 on air = %q, want the repeater module", got)
			}
			if heard.frames < 10 {
				t.Errorf("only %d voice frames reached the repeater", heard.frames)
			}
		})
		time.Sleep(time.Second) // let the gateway release the module
	}

	// Control: the old reflector layout (RPT2 = "REF001 B") must be dropped.
	t.Run("ReflectorLayoutDropped", func(t *testing.T) {
		heard := gwTransmit(t, rf, dplus.New(), dplus.DefaultPort, Config{
			ReflectorCall: "REF001", ReflectorModule: gwModule, Protocol: protocol.ProtoDPlus,
		})
		if heard.header != nil || heard.frames > 0 {
			t.Errorf("gateway put a reflector-addressed transmission on the air (%d frames)", heard.frames)
		}
	})
}

// The router sends a radio's one-letter UR command ("       E") as CQCQCQ:
// gateways only run commands from their own RF. This sends the raw command
// to DStarGateway, bypassing that rewrite, and checks that the gateway
// relays it as an ordinary call without playing anything back.
func TestGatewayURCommandRelayed(t *testing.T) {
	rf, err := net.ListenUDP("udp4", &net.UDPAddr{Port: gwRFPort})
	if err != nil {
		t.Fatalf("listen for the gateway's repeater traffic: %v", err)
	}
	defer rf.Close()

	const nFrames = 2 * (dstar.MaxSeq + 1)
	heard := gwTransmitUR(t, rf, &rawURReflector{dplus.New(), "       E"}, dplus.DefaultPort, Config{
		ReflectorCall: gwRepeater, ReflectorModule: gwModule, Repeater: true, Protocol: protocol.ProtoDPlus,
	}, "       E", 6*time.Second)
	if heard.header == nil {
		t.Fatal("gateway put no header on the air")
	}
	h := heard.header
	t.Logf("on air: UR=%q MY=%q, %d headers / %d frames", h[27:35], h[35:43], heard.headers, heard.frames)
	if heard.frames < 10 {
		t.Errorf("only %d voice frames reached the repeater", heard.frames)
	}
	if got := string(h[27:35]); got != dstar.CQCall {
		t.Errorf("UR on air = %q, want the gateway to relay it as %q", got, dstar.CQCall)
	}
	if heard.frames > nFrames+10 || len(heard.linkRX) > 0 {
		t.Errorf("gateway played something back (%d frames, %d link headers): it does run link commands",
			heard.frames, len(heard.linkRX))
	}
}

// rawURReflector puts urcall back into headers after the router's rewrite.
type rawURReflector struct {
	protocol.Reflector
	urcall string
}

func (r *rawURReflector) SendHeader(hdr dstar.DVHeader) error {
	hdr.YourCall = r.urcall
	return r.Reflector.SendHeader(hdr)
}
