//go:build !linux

package radio

import (
	"fmt"

	"go.bug.st/serial"
)

// BTDevice represents a paired Bluetooth device.
type BTDevice struct {
	Addr string
	Name string
}

// IsBTAddress returns true if s looks like a Bluetooth MAC address.
// On non-Linux platforms, Bluetooth RFCOMM is not supported so this
// always returns false.
func IsBTAddress(string) bool { return false }

// ListBTDevices returns nil on non-Linux platforms.
func ListBTDevices() []BTDevice { return nil }

// OpenRFCOMM is not supported on non-Linux platforms.
func OpenRFCOMM(addr string) (serial.Port, error) {
	return nil, fmt.Errorf("bluetooth RFCOMM is only supported on Linux")
}
