package router

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/S7R4nG3/refconnect/internal/aprs"
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

// fakeReflector lets the test inject RX traffic and records TX frames.
type fakeReflector struct {
	hdrCh chan dstar.DVHeader
	frmCh chan dstar.DVFrame
	evCh  chan protocol.Event
	txCh  chan dstar.DVFrame // frames the router transmitted
}

func newFakeReflector() *fakeReflector {
	return &fakeReflector{hdrCh: make(chan dstar.DVHeader), frmCh: make(chan dstar.DVFrame),
		evCh: make(chan protocol.Event), txCh: make(chan dstar.DVFrame, 256)}
}

func (f *fakeReflector) Connect(protocol.Config) error    { return nil }
func (f *fakeReflector) Disconnect() error                { return nil }
func (f *fakeReflector) State() protocol.LinkState        { return protocol.StateConnected }
func (f *fakeReflector) SendHeader(dstar.DVHeader) error  { return nil }
func (f *fakeReflector) SendFrame(fr dstar.DVFrame) error { f.txCh <- fr; return nil }
func (f *fakeReflector) RxHeaders() <-chan dstar.DVHeader { return f.hdrCh }
func (f *fakeReflector) RxFrames() <-chan dstar.DVFrame   { return f.frmCh }
func (f *fakeReflector) Events() <-chan protocol.Event    { return f.evCh }

func startRouter(t *testing.T) (*Router, *fakeRadio, *fakeReflector) {
	t.Helper()
	return startRouterWith(t, Config{})
}

func startRouterWith(t *testing.T, cfg Config) (*Router, *fakeRadio, *fakeReflector) {
	t.Helper()
	rad, ref := newFakeRadio(), newFakeReflector()
	rt := New(rad, ref, cfg)
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

// transmit sends a header and n voice frames from the radio, returning the
// frames the router forwarded to the reflector.
func transmit(t *testing.T, rad *fakeRadio, ref *fakeReflector, n int, radioSlow [3]byte) []dstar.DVFrame {
	t.Helper()
	rad.hdrCh <- dstar.DVHeader{MyCall: "KR4GCQ  ", YourCall: "CQCQCQ  ", RPT1: "KR4GCQ D", RPT2: "REF001 C"}
	var out []dstar.DVFrame
	for i := 0; i < n; i++ {
		seq := uint8(i % (dstar.MaxSeq + 1))
		f := dstar.DVFrame{Seq: seq, SlowData: radioSlow, End: i == n-1}
		if seq == 0 {
			f.SlowData = dstar.SyncSlowData
		}
		rad.frmCh <- f
		select {
		case fr := <-ref.txCh:
			out = append(out, fr)
		case <-time.After(2 * time.Second):
			t.Fatalf("frame %d not forwarded", i)
		}
	}
	return out
}

const testTNC2 = "KR4GCQ-1>APDPRS,DSTAR*:!3513.91N/08051.27W[RefConnect"

func TestTXCarriesTextThenDPRSPosition(t *testing.T) {
	_, rad, ref := startRouterWith(t, Config{
		TXText: "RefConnect by KR4GCQ",
		DPRS:   func() (string, bool) { return testTNC2, true },
	})
	frames := transmit(t, rad, ref, 5*(dstar.MaxSeq+1), [3]byte{0x16, 0x29, 0xF5})

	text := dstar.EncodeTextMessage("RefConnect by KR4GCQ")
	for i := 0; i <= dstar.MaxSeq; i++ {
		if frames[i].SlowData != text[i] {
			t.Fatalf("first superframe frame %d = % X, want text message % X", i, frames[i].SlowData, text[i])
		}
	}

	// A reflector decoding the rest of the stream finds our position.
	var dec dstar.DPRSDecoder
	var sentences []string
	for _, f := range frames[dstar.MaxSeq+1:] {
		sentences = append(sentences, dec.Feed(f.SlowData, f.Seq)...)
	}
	if len(sentences) == 0 {
		t.Fatal("no DPRS sentence in the transmission")
	}
	payload, ok := aprs.ValidateDPRS(sentences[0])
	if !ok || payload != testTNC2 {
		t.Fatalf("DPRS sentence %q: payload=%q valid=%v, want %q", sentences[0], payload, ok, testTNC2)
	}
	for i, f := range frames {
		if f.Seq == 0 && f.SlowData != dstar.SyncSlowData {
			t.Fatalf("frame %d (seq 0) lost its sync pattern", i)
		}
	}
}

func TestTXPassesRadioSlowDataWithoutPosition(t *testing.T) {
	radioSlow := [3]byte{0x16, 0x29, 0xF5}
	_, rad, ref := startRouterWith(t, Config{
		TXText: "RefConnect by KR4GCQ",
		DPRS:   func() (string, bool) { return "", false },
	})
	frames := transmit(t, rad, ref, 3*(dstar.MaxSeq+1), radioSlow)
	for i, f := range frames[dstar.MaxSeq+1:] {
		want := radioSlow
		if f.Seq == 0 {
			want = dstar.SyncSlowData
		}
		if f.SlowData != want {
			t.Fatalf("frame %d after the first superframe = % X, want the radio's % X", dstar.MaxSeq+1+i, f.SlowData, want)
		}
	}
}

// scrambledBlock returns the two scrambled frames carrying one 6-byte block.
func scrambledBlock(seg [6]byte, seq uint8) (a, b [3]byte) {
	return dstar.ScrambleSlowData([3]byte{seg[0], seg[1], seg[2]}, seq),
		dstar.ScrambleSlowData([3]byte{seg[3], seg[4], seg[5]}, seq+1)
}

// A radio's own TX message (interleaved with its GPS, as the IC-705 does)
// must not leak through after the first superframe; its GPS must.
func TestTXPassThroughBlanksRadioTextKeepsGPS(t *testing.T) {
	_, rad, ref := startRouterWith(t, Config{
		TXText: "RefConnect by KR4GCQ",
		DPRS:   func() (string, bool) { return "", false },
	})
	gps := [6]byte{0x35, '$', '$', 'C', 'R', 'C'}
	msg := [6]byte{0x40, 'D', 'a', 'v', 'e', ' '}

	rad.hdrCh <- dstar.DVHeader{MyCall: "KR4GCQ  ", YourCall: "CQCQCQ  ", RPT1: "KR4GCQ D", RPT2: "REF001 C"}
	var out []dstar.DVFrame
	for i := 0; i < 2*(dstar.MaxSeq+1); i++ {
		seq := uint8(i % (dstar.MaxSeq + 1))
		f := dstar.DVFrame{Seq: seq, SlowData: dstar.SyncSlowData}
		if seq != 0 {
			// Alternate GPS and message blocks: 1-2 GPS, 3-4 message, ...
			first := seq
			if seq%2 == 0 {
				first = seq - 1
			}
			seg := gps
			if (first/2)%2 == 1 {
				seg = msg
			}
			a, b := scrambledBlock(seg, first)
			f.SlowData = a
			if seq%2 == 0 {
				f.SlowData = b
			}
		}
		rad.frmCh <- f
		out = append(out, <-ref.txCh)
	}

	for i, f := range out[dstar.MaxSeq+1:] {
		seq := f.Seq
		if seq == 0 || seq%2 == 0 {
			continue
		}
		typ := dstar.ScrambleSlowData(f.SlowData, seq)[0] & 0xF0
		if typ == 0x40 {
			t.Fatalf("frame %d: radio text block passed through", dstar.MaxSeq+1+i)
		}
		if (seq/2)%2 == 0 && typ != 0x30 {
			t.Fatalf("frame %d: radio GPS block type %02X, want 0x3x", dstar.MaxSeq+1+i, typ)
		}
	}
}
