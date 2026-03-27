// Package audio provides a simple jitter buffer for smoothing out-of-order
// D-STAR DV voice frame delivery from the network.
package audio

import (
	"sync"

	"github.com/S7R4nG3/refconnect/internal/dstar"
)

// JitterBuffer holds a sliding window of DV voice frames, reordering them by
// sequence number before passing them downstream.  The buffer depth is fixed
// at construction time; each slot holds one 20 ms D-STAR frame.
//
// Call Push to add frames as they arrive from the network; call Pop to
// retrieve the next frame in sequence order (blocks until one is available
// or the buffer is flushed).
type JitterBuffer struct {
	mu     sync.Mutex
	depth  int
	slots  []slot
	nextSeq uint8
	readyCh chan dstar.DVFrame
	stopCh  chan struct{}
}

type slot struct {
	filled bool
	frame  dstar.DVFrame
}

// NewJitterBuffer creates a jitter buffer with the given frame depth (typically 3–8).
func NewJitterBuffer(depth int) *JitterBuffer {
	if depth < 1 {
		depth = 4
	}
	return &JitterBuffer{
		depth:   depth,
		slots:   make([]slot, dstar.MaxSeq),
		readyCh: make(chan dstar.DVFrame, depth*2),
		stopCh:  make(chan struct{}),
	}
}

// Push inserts a received frame into the buffer.
// Frames outside the current window are silently dropped.
func (b *JitterBuffer) Push(f dstar.DVFrame) {
	b.mu.Lock()
	defer b.mu.Unlock()

	idx := int(f.Seq) % dstar.MaxSeq
	b.slots[idx] = slot{filled: true, frame: f}
	b.drain()
}

// drain flushes consecutive ready frames starting from nextSeq into readyCh.
// Must be called with b.mu held.
func (b *JitterBuffer) drain() {
	for {
		idx := int(b.nextSeq) % dstar.MaxSeq
		if !b.slots[idx].filled {
			break
		}
		f := b.slots[idx].frame
		b.slots[idx] = slot{}
		b.nextSeq = (b.nextSeq + 1) % dstar.MaxSeq

		select {
		case b.readyCh <- f:
		default:
		}
		if f.End {
			b.nextSeq = 0
		}
	}
}

// Frames returns the channel of reordered frames ready for playback.
func (b *JitterBuffer) Frames() <-chan dstar.DVFrame { return b.readyCh }

// Flush discards all buffered frames and resets the sequence counter.
// Call at the start of each new transmission (on header receipt).
func (b *JitterBuffer) Flush() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := range b.slots {
		b.slots[i] = slot{}
	}
	b.nextSeq = 0
}
