package radio

import (
	"errors"
	"fmt"
	"testing"

	"go.bug.st/serial"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name         string
		port         DetectedPort
		ifNum        int
		wantModel    string
		wantProtocol string
		wantRole     string
	}{
		{"IC-705 DV data", DetectedPort{IsUSB: true, VID: "0c26", PID: "0036"}, 2, "Icom IC-705", ProtocolDVGateway, RoleDVData},
		{"IC-705 CI-V", DetectedPort{IsUSB: true, VID: "0C26", PID: "0036"}, 0, "Icom IC-705", ProtocolDVGateway, RoleCIV},
		{"IC-705 unknown iface", DetectedPort{IsUSB: true, VID: "0C26", PID: "0036"}, -1, "Icom IC-705", ProtocolDVGateway, ""},
		{"ID-52", DetectedPort{IsUSB: true, VID: "0C26", PID: "003A"}, 0, "Icom ID-52", ProtocolDVGateway, ""},
		{"TH-D75", DetectedPort{IsUSB: true, VID: "2166", PID: "9023"}, 0, "Kenwood TH-D75", ProtocolMMDVM, ""},
		{"unknown Icom", DetectedPort{IsUSB: true, VID: "0C26", PID: "0099"}, -1, "Icom radio", ProtocolDVGateway, ""},
		// The IC-9700's gateway runs over its DATA jack, not USB.
		{"IC-9700 B", DetectedPort{IsUSB: true, VID: "10C4", PID: "EA60", SerialNumber: "IC-9700 13001202 B"}, -1, "Icom IC-9700", "", ""},
		{"IC-9700 A", DetectedPort{IsUSB: true, VID: "10C4", PID: "EA60", SerialNumber: "IC-9700 13001202 A"}, -1, "Icom IC-9700", "", ""},
		{"IC-7300", DetectedPort{IsUSB: true, VID: "10C4", PID: "EA60", SerialNumber: "IC-7300 03001507"}, -1, "Icom IC-7300", "", ""},
		{"generic CP210x", DetectedPort{IsUSB: true, VID: "10C4", PID: "EA60", SerialNumber: "0001"}, -1, "", "", ""},
		{"non-USB", DetectedPort{}, -1, "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := tt.port
			classify(&p, tt.ifNum)
			if p.Model != tt.wantModel || p.Protocol != tt.wantProtocol || p.Role != tt.wantRole {
				t.Errorf("got model=%q protocol=%q role=%q, want %q %q %q",
					p.Model, p.Protocol, p.Role, tt.wantModel, tt.wantProtocol, tt.wantRole)
			}
		})
	}
}

func TestSortPortsPrefersDVData(t *testing.T) {
	civ := DetectedPort{Name: "/dev/cu.usbmodem12401", IsUSB: true, VID: "0C26", PID: "0036"}
	dv := DetectedPort{Name: "/dev/cu.usbmodem12403", IsUSB: true, VID: "0C26", PID: "0036"}
	other := DetectedPort{Name: "/dev/cu.usbserial-1", IsUSB: true, VID: "0403", PID: "6001"}
	classify(&civ, 0)
	classify(&dv, 2)
	classify(&other, -1)

	ports := []DetectedPort{other, civ, dv}
	SortPorts(ports)
	if ports[0].Name != dv.Name {
		t.Fatalf("best port = %s, want DV data port %s", ports[0].Name, dv.Name)
	}

	// A verified port outranks an unverified DV-data guess.
	civ.MarkVerified()
	ports = []DetectedPort{dv, civ}
	SortPorts(ports)
	if ports[0].Name != civ.Name {
		t.Fatalf("best port = %s, want verified port %s", ports[0].Name, civ.Name)
	}
}

func TestContainsPollAck(t *testing.T) {
	tests := []struct {
		name string
		b    []byte
		want bool
	}{
		{"ack", []byte{0x03, 0x03, 0x00, 0xFF}, true},
		{"ack ready", []byte{0x03, 0x03, 0x01, 0xFF}, true},
		{"after init echo", []byte{0xFF, 0xFF, 0xFF, 0x03, 0x03, 0x00, 0xFF}, true},
		{"partial", []byte{0x03, 0x03, 0x00}, false},
		{"false start then ack", []byte{0x03, 0x03, 0x03, 0x03, 0x00, 0xFF}, true},
		{"echoed poll only", []byte{0x02, 0x02, 0xFF}, false},
		{"empty", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsPollAck(tt.b); got != tt.want {
				t.Errorf("containsPollAck(% 02X) = %v, want %v", tt.b, got, tt.want)
			}
		})
	}
}

func TestLabel(t *testing.T) {
	p := DetectedPort{Name: "/dev/cu.usbmodem12403", Model: "Icom IC-705", Role: RoleDVData, Protocol: ProtocolDVGateway}
	if got, want := p.Label(), "Icom IC-705 · DV data (cu.usbmodem12403)"; got != want {
		t.Errorf("Label() = %q, want %q", got, want)
	}
	p.MarkVerified()
	if got, want := p.Label(), "✓ Icom IC-705 · DV data (cu.usbmodem12403)"; got != want {
		t.Errorf("verified Label() = %q, want %q", got, want)
	}
	if got, want := (DetectedPort{Name: "COM1"}).Label(), "COM1"; got != want {
		t.Errorf("non-USB Label() = %q, want %q", got, want)
	}
}

func TestClassifyBluetooth(t *testing.T) {
	tests := []struct {
		btName       string
		wantModel    string
		wantProtocol string
		wantLabel    string
	}{
		{"TH-D75", "Kenwood TH-D75", ProtocolMMDVM, "Kenwood TH-D75 · Bluetooth (rfcomm0)"},
		{"th-d75 shack", "Kenwood TH-D75", ProtocolMMDVM, "Kenwood TH-D75 · Bluetooth (rfcomm0)"},
		// Icom radios can't run terminal mode over Bluetooth.
		{"IC-705", "", "", "IC-705 · Bluetooth (rfcomm0)"},
		{"ID-52PLUS", "", "", "ID-52PLUS · Bluetooth (rfcomm0)"},
		{"Pixel 7", "", "", "Pixel 7 · Bluetooth (rfcomm0)"},
		{"", "", "", "Bluetooth device AA:BB:CC:DD:EE:FF (rfcomm0)"},
	}
	for _, tt := range tests {
		t.Run(tt.btName, func(t *testing.T) {
			p := DetectedPort{Name: "/dev/rfcomm0", Bluetooth: true, BTAddr: "AA:BB:CC:DD:EE:FF", BTName: tt.btName}
			classifyBluetooth(&p)
			if p.Model != tt.wantModel || p.Protocol != tt.wantProtocol {
				t.Errorf("got model=%q protocol=%q, want %q %q", p.Model, p.Protocol, tt.wantModel, tt.wantProtocol)
			}
			if got := p.Label(); got != tt.wantLabel {
				t.Errorf("Label() = %q, want %q", got, tt.wantLabel)
			}
		})
	}

	// The same radio on USB outranks it on Bluetooth.
	usb := DetectedPort{Name: "/dev/ttyACM0", IsUSB: true, VID: "2166", PID: "9023"}
	bt := DetectedPort{Name: "/dev/rfcomm0", Bluetooth: true, BTName: "TH-D75"}
	classify(&usb, 0)
	classifyBluetooth(&bt)
	ports := []DetectedPort{bt, usb}
	SortPorts(ports)
	if ports[0].Name != usb.Name {
		t.Errorf("best port = %s, want USB %s", ports[0].Name, usb.Name)
	}
}

func TestPortErrorClassification(t *testing.T) {
	busy := &serial.PortError{} // zero value is the PortBusy code
	if busy.Code() != serial.PortBusy {
		t.Fatalf("zero PortError code = %v, want PortBusy", busy.Code())
	}
	if !IsPortBusy(busy) || !IsPortBusy(fmt.Errorf("probe: %w", busy)) {
		t.Error("busy PortError not recognised")
	}
	if IsPermissionDenied(busy) {
		t.Error("busy PortError classified as permission denied")
	}
	if IsPortBusy(errors.New("no such file")) || IsPortBusy(nil) || IsPermissionDenied(nil) {
		t.Error("non-PortError classified as busy/denied")
	}
}
