//go:build linux

package radio

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"go.bug.st/serial"
)

const (
	afBluetooth   = 31 // AF_BLUETOOTH
	btprotoRFCOMM = 3  // BTPROTO_RFCOMM
	sppChannel    = 1  // Standard Serial Port Profile RFCOMM channel
)

// BTDevice represents a paired Bluetooth device.
type BTDevice struct {
	Addr string // MAC address (e.g. "AA:BB:CC:DD:EE:FF")
	Name string // Human-readable device name
}

// IsBTAddress returns true if s looks like a Bluetooth MAC address.
func IsBTAddress(s string) bool {
	hw, err := net.ParseMAC(s)
	return err == nil && len(hw) == 6
}

// ListBTDevices returns paired Bluetooth devices by querying bluetoothctl.
// Returns nil if bluetoothctl is not available or no devices are paired.
func ListBTDevices() []BTDevice {
	out, err := exec.Command("bluetoothctl", "devices", "Paired").Output()
	if err != nil {
		return nil
	}
	var devices []BTDevice
	for _, line := range strings.Split(string(out), "\n") {
		// Format: "Device AA:BB:CC:DD:EE:FF Device Name"
		parts := strings.SplitN(strings.TrimSpace(line), " ", 3)
		if len(parts) < 3 || parts[0] != "Device" {
			continue
		}
		if !IsBTAddress(parts[1]) {
			continue
		}
		devices = append(devices, BTDevice{Addr: parts[1], Name: parts[2]})
	}
	return devices
}

// OpenRFCOMM connects to a Bluetooth device via RFCOMM and returns a
// serial.Port-compatible wrapper. The device must be paired beforehand
// via system Bluetooth settings.
func OpenRFCOMM(addr string) (serial.Port, error) {
	hw, err := net.ParseMAC(addr)
	if err != nil {
		return nil, fmt.Errorf("bluetooth: invalid address %q: %w", addr, err)
	}

	fd, err := syscall.Socket(afBluetooth, syscall.SOCK_STREAM, btprotoRFCOMM)
	if err != nil {
		return nil, fmt.Errorf("bluetooth: socket: %w (is Bluetooth enabled?)", err)
	}

	// Build sockaddr_rc: family(2) + bdaddr(6) + channel(1) = 9 bytes.
	// bdaddr is stored in reverse byte order per BlueZ convention.
	var sa [9]byte
	*(*uint16)(unsafe.Pointer(&sa[0])) = afBluetooth
	for i := 0; i < 6; i++ {
		sa[2+i] = hw[5-i]
	}
	sa[8] = sppChannel

	log.Printf("bluetooth: connecting to %s on RFCOMM channel %d", addr, sppChannel)
	_, _, errno := syscall.Syscall(
		syscall.SYS_CONNECT,
		uintptr(fd),
		uintptr(unsafe.Pointer(&sa[0])),
		9,
	)
	if errno != 0 {
		syscall.Close(fd) //nolint:errcheck
		return nil, fmt.Errorf("bluetooth: connect to %s: %w", addr, errno)
	}
	log.Printf("bluetooth: connected to %s", addr)

	return &rfcommPort{fd: fd}, nil
}

// rfcommPort wraps a Bluetooth RFCOMM socket to satisfy the serial.Port interface.
type rfcommPort struct {
	fd int
}

func (p *rfcommPort) Read(buf []byte) (int, error) {
	for {
		n, err := syscall.Read(p.fd, buf)
		if err == syscall.EINTR {
			continue
		}
		// SO_RCVTIMEO expiry surfaces as EAGAIN — return (0, nil) to match
		// go.bug.st/serial timeout behavior.
		if err == syscall.EAGAIN || err == syscall.EWOULDBLOCK {
			return 0, nil
		}
		if n < 0 {
			n = 0
		}
		return n, err
	}
}

func (p *rfcommPort) Write(buf []byte) (int, error) {
	return syscall.Write(p.fd, buf)
}

func (p *rfcommPort) Close() error {
	return syscall.Close(p.fd)
}

func (p *rfcommPort) SetReadTimeout(t time.Duration) error {
	var tv syscall.Timeval
	if t > 0 {
		tv.Sec = int64(t / time.Second)
		tv.Usec = int64((t % time.Second) / time.Microsecond)
	}
	// Zero timeval = block indefinitely (clears timeout).
	return syscall.SetsockoptTimeval(p.fd, syscall.SOL_SOCKET, syscall.SO_RCVTIMEO, &tv)
}

// The following methods are no-ops for RFCOMM sockets — they only apply
// to physical serial ports.

func (p *rfcommPort) SetDTR(bool) error                                    { return nil }
func (p *rfcommPort) SetRTS(bool) error                                    { return nil }
func (p *rfcommPort) SetMode(*serial.Mode) error                           { return nil }
func (p *rfcommPort) Drain() error                                         { return nil }
func (p *rfcommPort) ResetInputBuffer() error                              { return nil }
func (p *rfcommPort) ResetOutputBuffer() error                             { return nil }
func (p *rfcommPort) GetModemStatusBits() (*serial.ModemStatusBits, error) { return &serial.ModemStatusBits{}, nil }
func (p *rfcommPort) Break(time.Duration) error                            { return nil }
