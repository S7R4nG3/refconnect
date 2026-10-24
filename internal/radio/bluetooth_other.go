//go:build !linux

package radio

// bluetoothInfo is only implemented on Linux (rfcomm + BlueZ). macOS
// Bluetooth serial is unusable for radios, and Windows Bluetooth COM ports
// are not identified yet.
func bluetoothInfo(string) (btDevice, bool) { return btDevice{}, false }
