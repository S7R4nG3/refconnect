package router

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"github.com/S7R4nG3/refconnect/internal/protocol"
	"github.com/S7R4nG3/refconnect/internal/radio"
)

// fakeRadio records what the router writes to it.
type fakeRadio struct {
	mu      sync.Mutex
	open    bool
	sendErr error // returned by SendHeader/SendFrame
	hdrCh   chan dstar.DVHeader
	frmCh   chan dstar.DVFrame
}

func newFakeRadio() *fakeRadio {
	return &fakeRadio{open: true, hdrCh: make(chan dstar.DVHeader), frmCh: make(chan dstar.DVFrame)}
}

func (r *fakeRadio) Open(radio.Config) error { return nil }
func (r *fakeRadio) Close() error            { return nil }
func (r *fakeRadio) IsOpen() bool            { r.mu.Lock(); defer r.mu.Unlock(); return r.open }
func (r *fakeRadio) SendHeader(dstar.DVHeader) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sendErr
}
func (r *fakeRadio) SendFrame(dstar.DVFrame) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sendErr
}
func (r *fakeRadio) RxHeaders() <-chan dstar.DVHeader { return r.hdrCh }
func (r *fakeRadio) RxFrames() <-chan dstar.DVFrame   { return r.frmCh }
func (r *fakeRadio) PTT(bool) error                   { return nil }

func (r *fakeRadio) setSendErr(err error) { r.mu.Lock(); r.sendErr = err; r.mu.Unlock() }

// fakeReflector lets the test inject RX traffic.
type fakeReflector struct {
	hdrCh chan dstar.DVHeader
	frmCh chan dstar.DVFrame
	evCh  chan protocol.Event
}

func newFakeReflector() *fakeReflector {
	return &fakeReflector{hdrCh: make(chan dstar.DVHeader), frmCh: make(chan dstar.DVFrame), evCh: make(chan protocol.Event)}
}

func (f *fakeReflector) Connect(protocol.Config) error    { return nil }
func (f *fakeReflector) Disconnect() error                { return nil }
func (f *fakeReflector) State() protocol.LinkState        { return protocol.StateConnected }
func (f *fakeReflector) SendHeader(dstar.DVHeader) error  { return nil }
func (f *fakeReflector) SendFrame(dstar.DVFrame) error    { return nil }
func (f *fakeReflector) RxHeaders() <-chan dstar.DVHeader { return f.hdrCh }
func (f *fakeReflector) RxFrames() <-chan dstar.DVFrame   { return f.frmCh }
func (f *fakeReflector) Events() <-chan protocol.Event    { return f.evCh }

func startRouter(t *testing.T) (*Router, *fakeRadio, *fakeReflector) {
	t.Helper()
	rad, ref := newFakeRadio(), newFakeReflector()
	rt := New(rad, ref, Config{})
	rt.Start()
	t.Cleanup(rt.Stop)
	return rt, rad, ref
}

// nextRX returns the next RX header/frame event, skipping others.
func nextRX(t *testing.T, rt *Router) Event {
	t.Helper()
	for {
		select {
		case e := <-rt.Events():
			if e.Direction == DirRX {
				return e
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no RX event")
		}
	}
}

var testHeader = dstar.DVHeader{MyCall: "VK5ACX  ", YourCall: "CQCQCQ  ", RPT1: "VK5ACX B", RPT2: "REF001 C"}

func TestRXFramesAfterHeaderAreDelivered(t *testing.T) {
	rt, _, ref := startRouter(t)

	ref.hdrCh <- testHeader
	if e := nextRX(t, rt); e.Header == nil || !e.Delivered {
		t.Fatalf("header event = %+v, want delivered header", e)
	}
	for i := 0; i < 3; i++ {
		ref.frmCh <- dstar.DVFrame{Seq: uint8(i)}
		if e := nextRX(t, rt); !e.Delivered || e.Note != "" {
			t.Fatalf("frame %d: Delivered=%v Note=%q, want delivered, no note", i, e.Delivered, e.Note)
		}
	}
}

func TestRXFramesWithoutHeaderNoteOncePerTransmission(t *testing.T) {
	rt, _, ref := startRouter(t)

	for stream := 0; stream < 2; stream++ {
		notes := 0
		for i := 0; i < 5; i++ {
			ref.frmCh <- dstar.DVFrame{Seq: uint8(i), End: i == 4}
			e := nextRX(t, rt)
			if e.Delivered {
				t.Fatalf("stream %d frame %d delivered without a header", stream, i)
			}
			if e.Note != "" {
				notes++
			}
		}
		if notes != 1 {
			t.Fatalf("stream %d: %d notes, want 1", stream, notes)
		}
	}
}

func TestRXLinkDownIsNotDeliveredOrAnError(t *testing.T) {
	rt, rad, ref := startRouter(t)
	rad.setSendErr(radio.ErrLinkDown)

	ref.hdrCh <- testHeader
	ref.frmCh <- dstar.DVFrame{}
	for i := 0; i < 2; i++ {
		e := nextRX(t, rt)
		if e.Err != nil || e.Delivered || e.Note != "" {
			t.Fatalf("event %d = %+v, want undelivered with no error or note", i, e)
		}
	}
}

func TestRXSendErrorIsReported(t *testing.T) {
	rt, rad, ref := startRouter(t)
	rad.setSendErr(errors.New("write failed"))

	ref.frmCh <- dstar.DVFrame{}
	if e := nextRX(t, rt); e.Err == nil {
		t.Fatalf("event = %+v, want the send error", e)
	}
	if e := nextRX(t, rt); e.Delivered {
		t.Fatal("failed frame reported as delivered")
	}
}

func TestRXStreamWithoutEndDoesNotCarryHeaderOver(t *testing.T) {
	gap := rxStreamGap
	rxStreamGap = 50 * time.Millisecond
	t.Cleanup(func() { rxStreamGap = gap })
	rt, _, ref := startRouter(t)

	ref.hdrCh <- testHeader
	nextRX(t, rt)
	ref.frmCh <- dstar.DVFrame{} // stream ends abruptly: no End frame
	if e := nextRX(t, rt); !e.Delivered {
		t.Fatal("first stream frame not delivered")
	}

	time.Sleep(2 * rxStreamGap)
	ref.frmCh <- dstar.DVFrame{} // a new, headerless stream
	if e := nextRX(t, rt); e.Delivered || e.Note == "" {
		t.Fatalf("new headerless stream: Delivered=%v Note=%q, want undelivered with note", e.Delivered, e.Note)
	}
}
