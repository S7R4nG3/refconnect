//go:build darwin || linux

package radio

import (
	"bytes"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"go.bug.st/serial"
)

// fakeModem answers MMDVM commands like a TH-D75 on the master side of a
// pty. While muted it swallows commands, like a radio that went quiet.
type fakeModem struct {
	master *os.File
	mute   atomic.Bool
	done   chan struct{}
}

func newFakeModem(t *testing.T) (*fakeModem, string) {
	t.Helper()
	master, slave, err := openPTY()
	if err != nil {
		t.Skipf("no pty available: %v", err)
	}
	f := &fakeModem{master: master, done: make(chan struct{})}
	go f.run()
	return f, slave
}

func (f *fakeModem) run() {
	defer close(f.done)
	buf := make([]byte, 256)
	var rx []byte
	for {
		n, err := f.master.Read(buf)
		if err != nil {
			if strings.Contains(err.Error(), "closed") {
				return
			}
			// No slave open right now (between reconnects); try again.
			time.Sleep(10 * time.Millisecond)
			continue
		}
		rx = append(rx, buf[:n]...)
		for {
			i := bytes.IndexByte(rx, mmdvmFrameStart)
			if i < 0 {
				rx = rx[:0]
				break
			}
			rx = rx[i:]
			if len(rx) < 3 {
				break
			}
			if rx[1] < 3 {
				rx = rx[1:]
				continue
			}
			if len(rx) < int(rx[1]) {
				break
			}
			cmd := rx[2]
			rx = rx[rx[1]:]
			if !f.mute.Load() {
				f.reply(cmd)
			}
		}
	}
}

func (f *fakeModem) reply(cmd byte) {
	switch cmd {
	case mmdvmGetVersion:
		mmdvmWriteFrame(f.master, mmdvmGetVersion, append([]byte{1}, "TH-D75 RTM1.00"...)) //nolint:errcheck
	case mmdvmGetStatus:
		mmdvmWriteFrame(f.master, mmdvmGetStatus, []byte{mmdvmModeDStar, 0, 0, 10}) //nolint:errcheck
	case mmdvmSetConfig, mmdvmSetMode, mmdvmSetFreq:
		mmdvmWriteFrame(f.master, mmdvmACK, []byte{cmd}) //nolint:errcheck
	}
}

// shortenLinkTimings speeds up the supervisor for tests.
func shortenLinkTimings(t *testing.T) {
	t.Helper()
	lt, ri, mi := linkTimeout, reopenInterval, modemInitDelay
	linkTimeout, reopenInterval, modemInitDelay = time.Second, 100*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { linkTimeout, reopenInterval, modemInitDelay = lt, ri, mi })
}

func waitNote(t *testing.T, notes <-chan string, want string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case got := <-notes:
			if got == want {
				return
			}
			t.Logf("note: %q", got)
		case <-deadline:
			t.Fatalf("no %q notification within %s", want, within)
		}
	}
}

func TestMMDVMReconnectsWhenRadioGoesSilent(t *testing.T) {
	shortenLinkTimings(t)
	modem, path := newFakeModem(t)
	defer modem.master.Close()

	r := NewMMDVMRadio()
	notes := make(chan string, 16)
	r.Notify = func(msg string) { notes <- msg }
	if err := r.Open(Config{Port: path}); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	modem.mute.Store(true)
	waitNote(t, notes, "Radio not responding — reconnecting…", 5*time.Second)
	modem.mute.Store(false)
	waitNote(t, notes, "Radio reconnected.", 15*time.Second)

	if err := r.SendFrame(dstar.DVFrame{}); err != nil {
		t.Errorf("SendFrame after reconnect: %v", err)
	}
}

func TestMMDVMCloseDuringReconnect(t *testing.T) {
	shortenLinkTimings(t)
	modem, path := newFakeModem(t)

	r := NewMMDVMRadio()
	notes := make(chan string, 16)
	r.Notify = func(msg string) { notes <- msg }
	if err := r.Open(Config{Port: path}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Pull the plug: the pty disappears, as /dev/rfcomm0 does when the
	// Bluetooth link drops.
	modem.master.Close()
	<-modem.done
	select {
	case got := <-notes:
		if !strings.HasPrefix(got, "Radio ") || !strings.HasSuffix(got, "reconnecting…") {
			t.Fatalf("unexpected note %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no link-lost notification")
	}
	// Frames sent while the link is down are dropped and reported as such.
	if err := r.SendHeader(dstar.DVHeader{MyCall: "N0CALL  ", YourCall: "CQCQCQ  "}); !errors.Is(err, ErrLinkDown) {
		t.Errorf("SendHeader while link down = %v, want ErrLinkDown", err)
	}

	done := make(chan struct{})
	go func() { r.Close(); close(done) }() //nolint:errcheck
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close hung while reconnecting")
	}
}

// writeCountingPort records writes; other serial.Port methods are unused.
type writeCountingPort struct {
	serial.Port
	writes atomic.Int32
}

func (p *writeCountingPort) Write(b []byte) (int, error) {
	p.writes.Add(1)
	return len(b), nil
}

func TestMMDVMDropsFramesUntilSessionStarts(t *testing.T) {
	port := &writeCountingPort{}
	r := NewMMDVMRadio()
	r.open = true
	r.port = port // port open, handshake still running: no session yet

	if err := r.SendHeader(dstar.DVHeader{MyCall: "N0CALL  ", YourCall: "CQCQCQ  "}); !errors.Is(err, ErrLinkDown) {
		t.Fatalf("SendHeader = %v, want ErrLinkDown", err)
	}
	if err := r.SendFrame(dstar.DVFrame{}); !errors.Is(err, ErrLinkDown) {
		t.Fatalf("SendFrame = %v, want ErrLinkDown", err)
	}
	if n := port.writes.Load(); n != 0 {
		t.Fatalf("%d writes reached the radio before its session started", n)
	}

	r.sessStop = make(chan struct{}) // session running
	if err := r.SendFrame(dstar.DVFrame{}); err != nil {
		t.Fatalf("SendFrame: %v", err)
	}
	if port.writes.Load() == 0 {
		t.Fatal("frame not written once the session was running")
	}
}
