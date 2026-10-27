package radio

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"go.bug.st/serial"
	"go.bug.st/serial/enumerator"
)

// Port auto-detection.
//
// Known radios are identified by USB VID/PID, or by the USB serial-number
// string for radios built on a generic Silicon Labs CP210x bridge (Icom sets
// it to e.g. "IC-9700 13001202 A"). Radios that expose several serial ports
// (IC-705: CI-V + DV data) are disambiguated by USB interface number where
// the OS reveals it, and by ProbeDVGateway everywhere else (Windows).

// Protocol values match config.RadioConfig.Protocol.
const (
	ProtocolDVGateway = "DV-GW"
	ProtocolMMDVM     = "MMDVM"
)

// Port roles for radios with more than one serial interface.
const (
	RoleDVData = "DV data"
	RoleCIV    = "CI-V"
)

// DetectedPort describes one serial port and what radio, if any, is behind it.
type DetectedPort struct {
	Name         string // OS port path, e.g. /dev/cu.usbmodem12403 or COM5
	Model        string // e.g. "Icom IC-705"; "" when unrecognised
	Protocol     string // ProtocolDVGateway, ProtocolMMDVM, or "" when unknown
	Role         string // RoleDVData, RoleCIV, or "" when not determinable
	IsUSB        bool
	VID, PID     string // upper-case hex, "" for non-USB ports
	SerialNumber string
	Product      string // USB product string, when the OS reports it
	Bluetooth    bool   // Bluetooth serial port (Linux rfcomm)
	BTAddr       string // remote Bluetooth address, when known
	BTName       string // remote Bluetooth device name, when known
	Verified     bool   // radio answered a DV Gateway poll on this port
	Score        int    // higher = more likely the radio's DV data port
}

// Label returns a human-friendly name for display in the port selector.
func (p DetectedPort) Label() string {
	base := filepath.Base(p.Name)
	var desc string
	switch {
	case p.Model != "" && p.Role != "":
		desc = p.Model + " · " + p.Role
	case p.Model != "" && p.Bluetooth:
		desc = p.Model + " · Bluetooth"
	case p.Model != "":
		desc = p.Model
	case p.Bluetooth && p.BTName != "":
		desc = p.BTName + " · Bluetooth"
	case p.Bluetooth && p.BTAddr != "":
		desc = "Bluetooth device " + p.BTAddr
	case p.Bluetooth:
		desc = "Bluetooth device"
	case p.IsUSB && p.Product != "":
		desc = p.Product
	case p.IsUSB:
		desc = "USB serial device"
	default:
		return base
	}
	if p.Verified {
		desc = "✓ " + desc
	}
	return fmt.Sprintf("%s (%s)", desc, base)
}

// IsRadio reports whether the port belongs to a recognised D-STAR radio.
func (p DetectedPort) IsRadio() bool { return p.Protocol != "" }

type knownRadio struct {
	vid, pid string
	model    string
	protocol string
	// roles maps USB interface number to port role for multi-port radios.
	roles map[int]string
}

// knownRadios lists radios whose USB IDs have been confirmed from pcaps,
// driver INFs or user reports. Add new models here as they are verified.
var knownRadios = []knownRadio{
	{vid: "0C26", pid: "0036", model: "Icom IC-705", protocol: ProtocolDVGateway,
		roles: map[int]string{0: RoleCIV, 2: RoleDVData}},
	{vid: "0C26", pid: "003A", model: "Icom ID-52", protocol: ProtocolDVGateway},
	{vid: "2166", pid: "9023", model: "Kenwood TH-D75", protocol: ProtocolMMDVM},
}

// knownVendors catches newer models from the same manufacturer whose PIDs
// are not yet in knownRadios (IC-905, ID-50, ID-52 PLUS, …).
var knownVendors = map[string]knownRadio{
	"0C26": {model: "Icom radio", protocol: ProtocolDVGateway},
	"2166": {model: "Kenwood radio", protocol: ProtocolMMDVM},
}

// classify fills in Model/Protocol/Role/Score from the USB details.
// ifNum is the USB interface number, or -1 when unknown.
func classify(p *DetectedPort, ifNum int) {
	vid, pid := strings.ToUpper(p.VID), strings.ToUpper(p.PID)

	matched := false
	for _, k := range knownRadios {
		if k.vid == vid && k.pid == pid {
			p.Model, p.Protocol = k.model, k.protocol
			if ifNum >= 0 {
				p.Role = k.roles[ifNum]
			}
			p.Score = 50
			matched = true
			break
		}
	}
	if !matched {
		if k, ok := knownVendors[vid]; ok {
			p.Model, p.Protocol = k.model, k.protocol
			p.Score = 30
			matched = true
		}
	}
	if !matched && vid == "10C4" && pid == "EA60" {
		// Icom radios on a Silicon Labs bridge (IC-9700, IC-7300, IC-7100, …)
		// set the serial string to e.g. "IC-9700 13001202 A". They're labelled
		// so users can tell them apart, but none is a DV Gateway candidate:
		// the IC-9700's External Gateway runs over its DATA jack (OPC-2350LU
		// cable), not its USB port.
		fields := strings.Fields(p.SerialNumber)
		if len(fields) > 0 && (strings.HasPrefix(fields[0], "IC-") || strings.HasPrefix(fields[0], "ID-")) {
			p.Model = "Icom " + fields[0]
			matched = true
		}
	}

	switch {
	case !matched && p.IsUSB:
		p.Score = 5
	case p.Role == RoleDVData:
		p.Score += 20
	case p.Role == RoleCIV:
		p.Score -= 20
	}
}

// btDevice is the remote end of a Bluetooth serial port.
type btDevice struct {
	Addr string // e.g. "AA:BB:CC:DD:EE:FF"
	Name string // BlueZ alias/name, e.g. "TH-D75"
}

// bluetoothModels maps a substring of a Bluetooth device name to the radio.
// Only radios whose Bluetooth link carries their terminal-mode protocol: the
// IC-705, ID-52 and ID-50 run DV Gateway Terminal mode over USB only, and the
// ID-52 PLUS is only documented with Icom's own apps over Bluetooth.
var bluetoothModels = []knownRadio{
	{vid: "TH-D75", model: "Kenwood TH-D75", protocol: ProtocolMMDVM},
}

// classifyBluetooth identifies a radio from its Bluetooth device name.
// Bluetooth scores just below the same radio on USB, which is more reliable.
func classifyBluetooth(p *DetectedPort) {
	name := strings.ToUpper(p.BTName)
	for _, k := range bluetoothModels {
		if strings.Contains(name, k.vid) {
			p.Model, p.Protocol = k.model, k.protocol
			p.Score = 45
			return
		}
	}
	p.Score = 5
}

// applyBluetooth fills in Bluetooth details for a non-USB port, if it is one.
func applyBluetooth(p *DetectedPort) {
	if p.IsUSB {
		return
	}
	bt, ok := bluetoothInfo(p.Name)
	if !ok {
		return
	}
	p.Bluetooth, p.BTAddr, p.BTName = true, bt.Addr, bt.Name
	classifyBluetooth(p)
}

// hiddenPorts are macOS pseudo-ports that are never a radio.
var hiddenPorts = []string{"Bluetooth-Incoming-Port", "debug-console", "wlan-debug"}

func isHidden(name string) bool {
	for _, h := range hiddenPorts {
		if strings.Contains(name, h) {
			return true
		}
	}
	// macOS lists every device twice; /dev/tty.* waits for carrier detect,
	// so only the /dev/cu.* callout device is usable.
	return runtime.GOOS == "darwin" && strings.HasPrefix(name, "/dev/tty.")
}

// interfaceNumber returns the USB interface number behind a port, or -1.
func interfaceNumber(name string) int {
	switch runtime.GOOS {
	case "linux":
		b, err := os.ReadFile(filepath.Join("/sys/class/tty", filepath.Base(name), "device", "bInterfaceNumber"))
		if err != nil {
			return -1
		}
		n, err := strconv.ParseInt(strings.TrimSpace(string(b)), 16, 32)
		if err != nil {
			return -1
		}
		return int(n)
	case "darwin":
		// CDC-ACM ports are named /dev/cu.usbmodem<location><ifnum+1>,
		// e.g. IC-705 → …1 (CI-V, interface 0) and …3 (DV data, interface 2).
		if !strings.Contains(name, "usbmodem") {
			return -1
		}
		last := name[len(name)-1]
		if last < '1' || last > '9' {
			return -1
		}
		return int(last-'0') - 1
	}
	return -1
}

// DetectPorts enumerates serial ports and identifies known radios.
// The result is sorted best candidate first.
func DetectPorts() []DetectedPort {
	var ports []DetectedPort
	seen := map[string]bool{}

	details, err := enumerator.GetDetailedPortsList()
	if err != nil {
		log.Printf("detect: detailed enumeration failed: %v", err)
	}
	for _, d := range details {
		if d.Name == "" || seen[d.Name] || isHidden(d.Name) {
			continue
		}
		seen[d.Name] = true
		p := DetectedPort{
			Name:         d.Name,
			IsUSB:        d.IsUSB,
			VID:          strings.ToUpper(d.VID),
			PID:          strings.ToUpper(d.PID),
			SerialNumber: d.SerialNumber,
			Product:      d.Product,
		}
		classify(&p, interfaceNumber(d.Name))
		applyBluetooth(&p)
		ports = append(ports, p)
	}

	// Pick up any ports the detailed enumerator missed.
	names, _ := serial.GetPortsList()
	for _, n := range names {
		if seen[n] || isHidden(n) {
			continue
		}
		seen[n] = true
		p := DetectedPort{Name: n}
		applyBluetooth(&p)
		ports = append(ports, p)
	}

	SortPorts(ports)
	return ports
}

// SortPorts orders ports best candidate first, then by name.
func SortPorts(ports []DetectedPort) {
	sort.SliceStable(ports, func(i, j int) bool {
		if ports[i].Score != ports[j].Score {
			return ports[i].Score > ports[j].Score
		}
		return ports[i].Name < ports[j].Name
	})
}

// MarkVerified records a successful probe and re-scores the port.
func (p *DetectedPort) MarkVerified() {
	if p.Verified {
		return
	}
	p.Verified = true
	p.Score += 100
}

// probeMu serialises probes with Open so a detection probe never holds a
// port the user is trying to connect on.
var probeMu sync.Mutex

// waitForProbe blocks until any in-flight detection probe has released its port.
func waitForProbe() {
	probeMu.Lock()
	defer probeMu.Unlock()
}

// ProbeDVGateway opens a port at the DV Gateway Terminal baud rate, sends the
// init flush and polls, and reports whether a poll ack (03 03 xx FF) arrived
// within timeout. A CI-V port ignores these bytes (CI-V frames start FE FE).
func ProbeDVGateway(name string, timeout time.Duration) (bool, error) {
	probeMu.Lock()
	defer probeMu.Unlock()

	p, err := serial.Open(name, &serial.Mode{
		BaudRate: 38400,
		DataBits: 8,
		StopBits: serial.OneStopBit,
		Parity:   serial.NoParity,
	})
	if err != nil {
		return false, err
	}
	var closeOnce sync.Once
	closePort := func() { closeOnce.Do(func() { p.Close() }) } //nolint:errcheck
	defer closePort()

	// Guard against a wedged Read (seen on macOS Bluetooth ports): closing
	// the port from another goroutine unblocks it.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-done:
		case <-time.After(timeout + time.Second):
			closePort()
		}
	}()

	if err := p.SetReadTimeout(100 * time.Millisecond); err != nil {
		return false, err
	}
	if err := writeInit(p); err != nil {
		return false, err
	}
	time.Sleep(100 * time.Millisecond)

	deadline := time.Now().Add(timeout)
	var rx []byte
	buf := make([]byte, 64)
	nextPoll := time.Now()
	for time.Now().Before(deadline) {
		if !time.Now().Before(nextPoll) {
			if err := writePoll(p); err != nil {
				return false, err
			}
			nextPoll = time.Now().Add(400 * time.Millisecond)
		}
		n, err := p.Read(buf)
		if n > 0 {
			rx = append(rx, buf[:n]...)
			if containsPollAck(rx) {
				return true, nil
			}
		}
		if err != nil {
			return false, err
		}
	}
	if len(rx) > 0 {
		log.Printf("detect: probe %s: no poll ack, got % 02X", name, rx)
	}
	return false, nil
}

// IsPortBusy reports whether err means another program has the port open.
// On Linux, ModemManager briefly opens every new ttyACM port this way.
func IsPortBusy(err error) bool {
	var pe *serial.PortError
	return errors.As(err, &pe) && pe.Code() == serial.PortBusy
}

// IsPermissionDenied reports whether err means the user may not open the
// port (on Linux: not in the dialout group).
func IsPermissionDenied(err error) bool {
	var pe *serial.PortError
	return errors.As(err, &pe) && pe.Code() == serial.PermissionDenied
}

// containsPollAck reports whether b contains a DV Gateway poll ack frame.
func containsPollAck(b []byte) bool {
	for i := 0; ; i++ {
		j := bytes.Index(b[i:], []byte{0x03, typePollAck})
		if j < 0 {
			return false
		}
		i += j
		if i+3 < len(b) && b[i+3] == frameTerminator {
			return true
		}
	}
}
