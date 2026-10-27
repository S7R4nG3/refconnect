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

// radioStream builds n frames of radio slow data from a repeating sequence
// of unscrambled blocks (one block per odd/even frame pair).
func radioStream(n int, blocks [][6]byte) []dstar.DVFrame {
	var frames []dstar.DVFrame
	next := 0
	var cur [6]byte
	for i := 0; i < n; i++ {
		seq := uint8(i % (dstar.MaxSeq + 1))
		f := dstar.DVFrame{Seq: seq, End: i == n-1}
		switch {
		case seq == 0:
			f.SlowData = dstar.SyncSlowData
		case seq%2 == 1:
			cur = blocks[next%len(blocks)]
			next++
			f.SlowData = dstar.ScrambleSlowData([3]byte{cur[0], cur[1], cur[2]}, seq)
		default:
			f.SlowData = dstar.ScrambleSlowData([3]byte{cur[3], cur[4], cur[5]}, seq)
		}
		frames = append(frames, f)
	}
	return frames
}

// transmit sends a header and the frames from the radio, returning the frames
// the router forwarded to the reflector.
func transmit(t *testing.T, rad *fakeRadio, ref *fakeReflector, frames []dstar.DVFrame) []dstar.DVFrame {
	t.Helper()
	rad.hdrCh <- dstar.DVHeader{MyCall: "KR4GCQ  ", YourCall: "CQCQCQ  ", RPT1: "KR4GCQ D", RPT2: "REF001 C"}
	var out []dstar.DVFrame
	for i, f := range frames {
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

// decodeSlowData reads forwarded frames the way a reflector does, returning
// the text message and any DPRS sentences.
func decodeSlowData(frames []dstar.DVFrame) (text string, dprs []string) {
	msg := []byte("                    ")
	var dec dstar.DPRSDecoder
	var first [3]byte
	for _, f := range frames {
		dprs = append(dprs, dec.Feed(f.SlowData, f.Seq)...)
		if f.Seq == 0 {
			continue
		}
		d := dstar.ScrambleSlowData(f.SlowData, f.Seq)
		if f.Seq%2 == 1 {
			first = d
			continue
		}
		if first[0]&0xF0 == dstar.BlockTypeMessage {
			n := int(first[0] & 0x03)
			copy(msg[n*5:], []byte{first[1], first[2], d[0], d[1], d[2]})
		}
	}
	return string(msg), dprs
}

const testTNC2 = "KR4GCQ-1>APDPRS,DSTAR*:!3513.91N/08051.26W[RefConnect"

var fillerOnly = [][6]byte{dstar.FillerBlock}

func TestTXSendsTextAndPositionWhenRadioSendsNeither(t *testing.T) {
	_, rad, ref := startRouterWith(t, Config{
		TXText: "RefConnect by KR4GCQ",
		DPRS:   func() (string, bool) { return testTNC2, true },
	})
	// Under one second, like the on-air test: both must still fit.
	out := transmit(t, rad, ref, radioStream(44, fillerOnly))

	text, dprs := decodeSlowData(out)
	if text != "RefConnect by KR4GCQ" {
		t.Errorf("text message = %q", text)
	}
	if len(dprs) == 0 {
		t.Fatal("no DPRS sentence in a 44-frame transmission")
	}
	if payload, ok := aprs.ValidateDPRS(dprs[0]); !ok || payload != testTNC2 {
		t.Errorf("DPRS %q: payload=%q valid=%v, want %q", dprs[0], payload, ok, testTNC2)
	}
	for i, f := range out {
		if f.Seq == 0 && f.SlowData != dstar.SyncSlowData {
			t.Fatalf("frame %d (seq 0) lost its sync pattern", i)
		}
	}
}

func TestTXWithoutPositionPassesRadioFiller(t *testing.T) {
	_, rad, ref := startRouterWith(t, Config{
		TXText: "RefConnect by KR4GCQ",
		DPRS:   func() (string, bool) { return "", false },
	})
	in := radioStream(3*(dstar.MaxSeq+1), fillerOnly)
	out := transmit(t, rad, ref, in)

	if text, dprs := decodeSlowData(out); text != "RefConnect by KR4GCQ" || len(dprs) != 0 {
		t.Errorf("text=%q dprs=%q, want our text and no DPRS", text, dprs)
	}
	// After the text message's four blocks, the radio's filler passes through.
	for i := 9; i < len(out); i++ {
		if out[i].SlowData != in[i].SlowData {
			t.Fatalf("frame %d = % X, want the radio's % X", i, out[i].SlowData, in[i].SlowData)
		}
	}
}

// An IC-705 with a TX message and GPS interleaves the two. Its message must
// be replaced by ours and its GPS must reach the reflector intact, rather
// than our position being mixed in.
func TestTXReplacesRadioTextKeepsRadioGPS(t *testing.T) {
	_, rad, ref := startRouterWith(t, Config{
		TXText: "RefConnect by KR4GCQ",
		DPRS:   func() (string, bool) { return testTNC2, true },
	})
	radioDPRS := aprs.WrapDPRS("KR4GCQ>API705,DSTAR*:!3513.91N/08051.26W[/")
	radioText := dstar.TextMessageBlocks("Dave - CLT, NC")
	gps := dstar.DPRSBlocks(radioDPRS)
	// Message, message, GPS, message, GPS, ... as captured from the IC-705.
	var blocks [][6]byte
	for i, g := range gps {
		blocks = append(blocks, radioText[i%4], g)
	}
	out := transmit(t, rad, ref, radioStream(4*(dstar.MaxSeq+1), blocks))

	text, dprs := decodeSlowData(out)
	if text != "RefConnect by KR4GCQ" {
		t.Errorf("text message = %q, want ours", text)
	}
	if len(dprs) == 0 || dprs[0] != radioDPRS {
		t.Errorf("DPRS = %q, want the radio's %q", dprs, radioDPRS)
	}
}
