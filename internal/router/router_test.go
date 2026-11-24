package router

import (
	"errors"
	"strings"
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
	headers int   // SendHeader calls
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
	r.headers++
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

func (r *fakeRadio) headerCount() int { r.mu.Lock(); defer r.mu.Unlock(); return r.headers }

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

func TestRXStalledStreamResendsHeader(t *testing.T) {
	gap := rxStreamGap
	rxStreamGap = 50 * time.Millisecond
	t.Cleanup(func() { rxStreamGap = gap })
	rt, rad, ref := startRouter(t)

	hdr := testHeader
	hdr.StreamID = 0x8139
	ref.hdrCh <- hdr
	nextRX(t, rt)
	ref.frmCh <- dstar.DVFrame{Seq: 0, StreamID: 0x8139}
	nextRX(t, rt)

	time.Sleep(2 * rxStreamGap) // the stream stalls, then resumes
	ref.frmCh <- dstar.DVFrame{Seq: 1, StreamID: 0x8139}
	e := nextRX(t, rt)
	if !e.Delivered || !strings.Contains(e.Note, "paused") {
		t.Fatalf("resumed frame: Delivered=%v Note=%q, want delivered with a pause note", e.Delivered, e.Note)
	}
	if n := rad.headerCount(); n != 2 {
		t.Fatalf("radio got %d headers, want 2 (original + re-send)", n)
	}
	ref.frmCh <- dstar.DVFrame{Seq: 2, StreamID: 0x8139, End: true}
	if e := nextRX(t, rt); !e.Delivered || e.Note != "" {
		t.Fatalf("next frame: Delivered=%v Note=%q, want delivered, no note", e.Delivered, e.Note)
	}

	// After End, the same stream ID is not resumed.
	time.Sleep(2 * rxStreamGap)
	ref.frmCh <- dstar.DVFrame{StreamID: 0x8139}
	if e := nextRX(t, rt); e.Delivered || e.Note != noteNoHeader {
		t.Fatalf("frame after End: Delivered=%v Note=%q, want undelivered with no-header note", e.Delivered, e.Note)
	}
	if n := rad.headerCount(); n != 2 {
		t.Fatalf("radio got %d headers, want 2", n)
	}
}

func TestRXNewStreamWithoutHeaderIsNotDelivered(t *testing.T) {
	rt, rad, ref := startRouter(t)

	hdr := testHeader
	hdr.StreamID = 0x1111
	ref.hdrCh <- hdr
	nextRX(t, rt)
	ref.frmCh <- dstar.DVFrame{StreamID: 0x1111}
	nextRX(t, rt)

	// A different stream with no gap and no header of its own.
	ref.frmCh <- dstar.DVFrame{StreamID: 0x2222}
	if e := nextRX(t, rt); e.Delivered || e.Note != noteNoHeader {
		t.Fatalf("other stream's frame: Delivered=%v Note=%q, want undelivered with no-header note", e.Delivered, e.Note)
	}
	ref.frmCh <- dstar.DVFrame{StreamID: 0x2222}
	if e := nextRX(t, rt); e.Delivered || e.Note != "" {
		t.Fatalf("its next frame: Delivered=%v Note=%q, want undelivered, no repeat note", e.Delivered, e.Note)
	}
	if n := rad.headerCount(); n != 1 {
		t.Fatalf("radio got %d headers, want 1", n)
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

// With APRS off, a radio sending its own GPS must not put the position on the
// air: its GPS blocks become filler, while its text is still replaced by ours.
// The radio's fix is still decoded into the GPS cache.
func TestTXHidePositionStripsRadioGPS(t *testing.T) {
	rt, rad, ref := startRouterWith(t, Config{
		TXText:       "RefConnect by KR4GCQ",
		DPRS:         func() (string, bool) { return testTNC2, true },
		HidePosition: func() bool { return true },
	})
	radioDPRS := aprs.WrapDPRS("KR4GCQ>API705,DSTAR*:!3513.91N/08051.26W[/")
	radioText := dstar.TextMessageBlocks("Dave - CLT, NC")
	var blocks [][6]byte
	for i, g := range dstar.DPRSBlocks(radioDPRS) {
		blocks = append(blocks, radioText[i%4], g)
	}
	out := transmit(t, rad, ref, radioStream(4*(dstar.MaxSeq+1), blocks))

	text, dprs := decodeSlowData(out)
	if text != "RefConnect by KR4GCQ" {
		t.Errorf("text message = %q, want ours", text)
	}
	if len(dprs) != 0 {
		t.Errorf("DPRS = %q, want none with the position hidden", dprs)
	}
	for i, f := range out {
		if f.Seq%2 == 1 && dstar.ScrambleSlowData(f.SlowData, f.Seq)[0]&0xF0 == dstar.BlockTypeGPS {
			t.Fatalf("frame %d carries a GPS block", i)
		}
	}
	if _, _, ok := rt.GPS().Get(); !ok {
		t.Error("radio's GPS fix was not cached")
	}
}

// The far end drops headers that don't name it the way it routes them.
func TestRewriteHeaderForLinkTarget(t *testing.T) {
	cases := []struct {
		name       string
		cfg        Config
		rpt1, rpt2 string
	}{
		{"reflector", Config{ReflectorCall: "REF001", ReflectorModule: 'C', Protocol: protocol.ProtoDPlus},
			"KR4GCQ D", "REF001 C"},
		{"repeater DPlus", Config{ReflectorCall: "W4ABC", ReflectorModule: 'B', Repeater: true, Protocol: protocol.ProtoDPlus},
			"W4ABC  B", "W4ABC  B"},
		{"repeater DExtra", Config{ReflectorCall: "W4ABC", ReflectorModule: 'B', Repeater: true, Protocol: protocol.ProtoDExtra},
			"W4ABC  B", "W4ABC  G"},
		{"repeater DCS", Config{ReflectorCall: "W4ABC", ReflectorModule: 'B', Repeater: true, Protocol: protocol.ProtoDCS},
			"KR4GCQ D", "W4ABC  B"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.cfg.MyCall = "KR4GCQ D"
			rt := New(newFakeRadio(), newFakeReflector(), c.cfg)
			hdr := dstar.DVHeader{MyCall: "KR4GCQ  ", YourCall: "       E", RPT1: "DIRECT  ", RPT2: "DIRECT  "}
			rt.rewriteHeaderForReflector(&hdr)
			if hdr.RPT1 != c.rpt1 || hdr.RPT2 != c.rpt2 {
				t.Errorf("RPT1=%q RPT2=%q, want %q %q", hdr.RPT1, hdr.RPT2, c.rpt1, c.rpt2)
			}
			if hdr.YourCall != dstar.CQCall {
				t.Errorf("URCALL=%q, want %q", hdr.YourCall, dstar.CQCall)
			}
		})
	}
}

func TestIsGatewayCommand(t *testing.T) {
	for in, want := range map[string]bool{
		"       E": true, "       I": true, "       U": true,
		"CQCQCQ  ": false, "W1AW    ": false, "REF001CL": false,
		"       e": false, "      EE": false, "": false,
	} {
		if got := isGatewayCommand(in); got != want {
			t.Errorf("isGatewayCommand(%q) = %v, want %v", in, got, want)
		}
	}
}

// A radio UR command is sent as CQCQCQ, with a note explaining why.
func TestTXGatewayCommandNote(t *testing.T) {
	rt, rad, _ := startRouterWith(t, Config{MyCall: "KR4GCQ D", ReflectorCall: "W4ABC", ReflectorModule: 'B',
		Repeater: true, Protocol: protocol.ProtoDPlus})
	rad.hdrCh <- dstar.DVHeader{MyCall: "KR4GCQ  ", YourCall: "       E", RPT1: "DIRECT  ", RPT2: "DIRECT  "}
	e := nextTXHeader(t, rt)
	if e.Header.YourCall != dstar.CQCall {
		t.Errorf("URCALL=%q, want %q", e.Header.YourCall, dstar.CQCall)
	}
	if !strings.Contains(e.Note, "echo test") || !strings.Contains(e.Note, "echo module") {
		t.Errorf("note = %q", e.Note)
	}

	rad.hdrCh <- dstar.DVHeader{MyCall: "KR4GCQ  ", YourCall: "CQCQCQ  ", RPT1: "DIRECT  ", RPT2: "DIRECT  "}
	if e := nextTXHeader(t, rt); e.Note != "" {
		t.Errorf("CQ header got a note: %q", e.Note)
	}
}

func nextTXHeader(t *testing.T, rt *Router) Event {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case e := <-rt.Events():
			if e.Direction == DirTX && e.Header != nil {
				return e
			}
		case <-timeout:
			t.Fatal("no TX header event")
		}
	}
}

// A REF reflector sends a DPlus link every module's traffic; only the linked
// module's streams reach the radio.
func TestRXOtherModuleStreamIsDropped(t *testing.T) {
	rt, rad, ref := startRouterWith(t, Config{ReflectorCall: "REF001", ReflectorModule: 'C'})

	off := testHeader
	off.RPT2, off.StreamID = "REF001 B", 0xB0B0
	ref.hdrCh <- off
	ref.frmCh <- dstar.DVFrame{StreamID: 0xB0B0}

	on := testHeader
	on.StreamID = 0xC0C0
	ref.hdrCh <- on
	if e := nextRX(t, rt); e.Header == nil || e.Header.StreamID != 0xC0C0 {
		t.Fatalf("first RX event = %+v, want module C header", e)
	}
	// Interleaved off-module voice, through to its End frame.
	for _, f := range []dstar.DVFrame{{StreamID: 0xB0B0}, {StreamID: 0xC0C0}, {StreamID: 0xB0B0, End: true}, {StreamID: 0xC0C0}} {
		ref.frmCh <- f
		if f.StreamID != 0xC0C0 {
			continue
		}
		if e := nextRX(t, rt); e.Frame == nil || e.Frame.StreamID != 0xC0C0 || !e.Delivered || e.Note != "" {
			t.Fatalf("next RX event = %+v, want delivered module C frame", e)
		}
	}
	if n := rad.headerCount(); n != 1 {
		t.Fatalf("radio got %d headers, want 1", n)
	}
}

func TestOffModule(t *testing.T) {
	refl := &Router{cfg: Config{ReflectorCall: "REF001", ReflectorModule: 'C'}}
	rptr := &Router{cfg: Config{ReflectorCall: "W4NYR", ReflectorModule: 'C', Repeater: true}}
	for _, tc := range []struct {
		rt         *Router
		rpt1, rpt2 string
		want       bool
	}{
		{refl, "K3NF   B", "REF001 C", false},
		{refl, "K3NF   B", "REF001 B", true},
		{refl, "K3NF   B", "REF001 D", true},
		{refl, "REF001 C", "REF001 G", false}, // G is a gateway suffix
		{refl, "REF001 A", "REF001 G", true},
		{refl, "K3NF   B", "REF054 B", false}, // another reflector: no opinion
		{refl, "K3NF   B", "", false},
		{rptr, "K3NF   B", "W4NYR  C", false},
		{rptr, "K3NF   B", "W4NYR  B", true},
		{rptr, "W4NYR  C", "W4NYR  G", false},
		{rptr, "W4NYR  A", "W4NYR  G", true},
		{rptr, "WD4MSM D", "W4FJ   B", false}, // another repeater: no opinion
	} {
		_, got := tc.rt.offModule(dstar.DVHeader{RPT1: tc.rpt1, RPT2: tc.rpt2})
		if got != tc.want {
			t.Errorf("offModule(RPT1=%q RPT2=%q, repeater=%v) = %v, want %v", tc.rpt1, tc.rpt2, tc.rt.cfg.Repeater, got, tc.want)
		}
	}
}
