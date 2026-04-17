// Package beacon synthesises a short D-STAR transmission that carries a
// DPRS position report in the slow-data stream and writes it directly to
// a connected reflector (bypassing the local radio). This matches the way
// most D-STAR gateways implement their own "RefConnect style" APRS beacons.
package beacon

import (
	"fmt"

	"github.com/S7R4nG3/refconnect/internal/dstar"
	"github.com/S7R4nG3/refconnect/internal/protocol"
)

// Send transmits a DPRS beacon to the given reflector. srcCall / rpt1 / rpt2
// are the routing fields used in the DVHeader; dprsSentence is the full
// "$$CRC....\r" sentence to embed in the slow-data stream.
//
// The caller is responsible for serializing beacon transmissions against
// any user-initiated voice TX so that frames do not interleave.
//
// Returns the DVHeader that was sent so the caller can announce it to ircDDB.
func Send(refl protocol.Reflector, srcCall, rpt1, rpt2, dprsSentence string) (dstar.DVHeader, error) {
	if refl == nil {
		return dstar.DVHeader{}, fmt.Errorf("beacon: no reflector")
	}
	if refl.State() != protocol.StateConnected {
		return dstar.DVHeader{}, fmt.Errorf("beacon: reflector not connected")
	}

	hdr := dstar.DVHeader{
		Flag1:    0x00, // forwarded, not TX-direction
		Flag2:    0x00,
		Flag3:    0x00,
		RPT2:     dstar.PadCallsign(rpt2, 8),
		RPT1:     dstar.PadCallsign(rpt1, 8),
		YourCall: dstar.CQCall,
		MyCall:   dstar.PadCallsign(srcCall, 8),
		MyCallSuffix: "    ",
	}
	if err := refl.SendHeader(hdr); err != nil {
		return hdr, fmt.Errorf("beacon: send header: %w", err)
	}

	// Start at seq=0 — EncodeDPRSFrames emits the sync frame automatically
	// and places GPS data in frames 1+.
	seq := uint8(0)
	slowFrames := dstar.EncodeDPRSFrames(dprsSentence, seq)
	for i, sd := range slowFrames {
		last := i == len(slowFrames)-1
		f := silenceFrame(seq, last)
		f.SlowData = sd
		if err := refl.SendFrame(f); err != nil {
			return hdr, fmt.Errorf("beacon: data frame: %w", err)
		}
		seq = (seq + 1) % (dstar.MaxSeq + 1)
	}
	return hdr, nil
}

// silenceFrame builds a DVFrame containing the D-STAR silence AMBE codeword
// and null slow data. Callers overwrite SlowData when they have DPRS
// content to embed.
func silenceFrame(seq uint8, end bool) dstar.DVFrame {
	return dstar.DVFrame{
		Seq:      seq,
		AMBE:     dstar.SilenceAMBE,
		SlowData: dstar.NullSlowData(seq),
		End:      end,
	}
}
