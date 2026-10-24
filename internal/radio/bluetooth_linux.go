//go:build linux

package radio

import (
	"context"
	"encoding/binary"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"
	"golang.org/x/sys/unix"
)

// rfcommGetDevInfo is RFCOMMGETDEVINFO, _IOR('R', 211, int), from
// <net/bluetooth/rfcomm.h>. The encoding matches amd64/arm/arm64/386.
const rfcommGetDevInfo = 0x800452D3

// bluetoothInfo returns the remote address and BlueZ name of the device an
// rfcomm port (/dev/rfcommN, from `rfcomm bind` or `rfcomm connect`) is bound
// to. It never opens the port itself, so it doesn't trigger a connection.
func bluetoothInfo(port string) (btDevice, bool) {
	base := filepath.Base(port)
	if !strings.HasPrefix(base, "rfcomm") {
		return btDevice{}, false
	}
	id, err := strconv.Atoi(strings.TrimPrefix(base, "rfcomm"))
	if err != nil {
		return btDevice{}, false
	}
	addr, err := rfcommAddr(id)
	if err != nil {
		// Still a Bluetooth port, just without details.
		return btDevice{}, true
	}
	return btDevice{Addr: addr, Name: bluezName(addr)}, true
}

// rfcommAddr asks the kernel which remote address rfcomm<id> is bound to.
func rfcommAddr(id int) (string, error) {
	fd, err := unix.Socket(unix.AF_BLUETOOTH, unix.SOCK_RAW, unix.BTPROTO_RFCOMM)
	if err != nil {
		return "", err
	}
	defer unix.Close(fd) //nolint:errcheck

	// struct rfcomm_dev_info {
	//   s16 id; u32 flags; u16 state;
	//   bdaddr_t src; bdaddr_t dst; u8 channel;
	// } — 24 bytes with padding; dst at offset 16.
	var di [24]byte
	binary.NativeEndian.PutUint16(di[0:2], uint16(id))
	if _, _, errno := unix.Syscall(unix.SYS_IOCTL, uintptr(fd), rfcommGetDevInfo, uintptr(unsafe.Pointer(&di[0]))); errno != 0 {
		return "", errno
	}
	dst := di[16:22] // bdaddr_t is stored least-significant byte first
	return fmt.Sprintf("%02X:%02X:%02X:%02X:%02X:%02X", dst[5], dst[4], dst[3], dst[2], dst[1], dst[0]), nil
}

var (
	bluezNamesMu sync.Mutex
	bluezNames   = map[string]string{} // address → name, successful lookups only
)

// bluezName looks up a paired device's name (Alias, else Name) over D-Bus.
// Returns "" if BlueZ isn't reachable or doesn't know the device.
func bluezName(addr string) string {
	bluezNamesMu.Lock()
	name, ok := bluezNames[addr]
	bluezNamesMu.Unlock()
	if ok {
		return name
	}

	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return ""
	}
	defer conn.Close() //nolint:errcheck

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	var objects map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	if err := conn.Object("org.bluez", "/").
		CallWithContext(ctx, "org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).
		Store(&objects); err != nil {
		return ""
	}
	for _, ifaces := range objects {
		dev, ok := ifaces["org.bluez.Device1"]
		if !ok {
			continue
		}
		if a, _ := dev["Address"].Value().(string); !strings.EqualFold(a, addr) {
			continue
		}
		for _, key := range []string{"Alias", "Name"} {
			if n, _ := dev[key].Value().(string); n != "" {
				bluezNamesMu.Lock()
				bluezNames[addr] = n
				bluezNamesMu.Unlock()
				return n
			}
		}
	}
	return ""
}
