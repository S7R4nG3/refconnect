# RefConnect Protocol Reference

This document captures everything known about the protocols used by RefConnect, derived from source code, pcap analysis, and the D-STAR specification. Pcaps are in `diagnostics/pcaps/`:

- `radio-comm.pcapng` — original capture, used for initial reverse engineering
- `doozy-cap.pcapng` — capture of Doozy (Windows app) talking to a working IC-705; used to verify and correct the implementation (2026-04-01)
- `rsms3w.pcapng` — capture of RS-MS3W (Windows) connecting to URF621A via IC-705; revealed 38400 baud and FF FF FF init sequence (2026-04-08)

---

## 1. Hardware Connection — Icom IC-705

> **Confirmed by Doozy pcap analysis (2026-04-01).** The IC-705 USB-B uses an internal Prolific PL2303 chip (VID/PID `0x0c26:0x0036`). The DV Gateway Terminal protocol works over **USB-B** — no external adapter is needed.

```
IC-705 ──► USB-B cable ──► Host USB
```

**Serial parameters:** **38400 baud**, 8 data bits, no parity, 1 stop bit (8N1). The RS-MS3W pcap confirms SET_LINE_CODING(38400) on the DV CDC interface. Although baud rate is nominally virtual on USB-CDC, the IC-705 firmware may use the SET_LINE_CODING value to select the DV Gateway Terminal mode — using 115200 or 9600 on macOS results in no response (only `FF` bytes returned).

### USB interface layout

The IC-705 presents as a single USB device with **two CDC-ACM virtual serial ports** (two IAD groups):

| Port | Interfaces | Endpoints | Function |
|------|-----------|-----------|----------|
| First  | 0 (CDC Control) + 1 (CDC Data) | 0x01 OUT / 0x02 IN | CI-V control |
| Second | 2 (CDC Control) + 3 (CDC Data) | 0x04 OUT / 0x85 IN | **DV Gateway Terminal data** |

On macOS, the two ports appear as `/dev/cu.usbmodem*1` and `/dev/cu.usbmodem*3` (the suffix digit matches the CDC Data interface number). The **`*3` port is the DV data port**.

**CDC setup required:** The RS-MS3W pcap shows SET_LINE_CODING(38400, 8N1) and SET_CONTROL_LINE_STATE(0) are sent before data transfer begins. On macOS, these are issued by the kernel CDC-ACM driver when the serial port is opened with the corresponding baud rate.

**Init flush:** Before sending the first poll, RS-MS3W sends `FF FF FF` (3 terminator bytes) on the DV data endpoint. The radio echoes back `FF FF FF`. This clears any partial frame state in the radio's protocol parser. Without this init, macOS gets only single `FF` bytes in response to polls.

**IC-705 radio settings:**
- MODE button → select **DV** (not FM/SSB/AM)
- `MENU > SET > CONNECTORS > ACC/USB OUTPUT SELECT` → **DV Data**

---

## 2. DV Gateway Terminal Serial Protocol

This is the binary framing protocol spoken over the serial link between the host and the IC-705. It was reverse-engineered from USBPcap captures and verified in full against the Doozy pcap.

### 2.1 Frame Structure

Every frame follows the same envelope:

```
[LEN][TYPE][DATA...][0xFF]
```

| Field | Size | Description |
|-------|------|-------------|
| LEN   | 1 byte | `total_frame_bytes − 1` (i.e. `LEN + 1 = total bytes`) |
| TYPE  | 1 byte | Frame type identifier (direction-specific) |
| DATA  | `LEN − 2` bytes | Frame payload |
| 0xFF  | 1 byte | Terminator, always the last byte |

### 2.2 Frame Types

| Constant         | Value | Direction      | Description                    |
|------------------|-------|----------------|--------------------------------|
| `typePoll`       | 0x02  | host → radio   | Keepalive poll                 |
| `typePollAck`    | 0x03  | radio → host   | Keepalive response             |
| `typeRXHeader`   | 0x10  | radio → host   | Received DV header from air    |
| `typeRXVoice`    | 0x12  | radio → host   | Received DV voice frame        |
| `typeTXHeader`   | 0x20  | host → radio   | Transmit DV header             |
| `typeTXHeaderAck`| 0x21  | radio → host   | TX header acknowledged         |
| `typeTXVoice`    | 0x22  | host → radio   | Transmit DV voice frame        |
| `typeTXVoiceAck` | 0x23  | radio → host   | TX voice frame acknowledged    |

### 2.3 Init Flush (host → radio) — 3 bytes

```
FF FF FF
```

Sent once before the first poll. Clears any residual parser state in the radio. The radio echoes back `FF FF FF`. Required on macOS; observed in RS-MS3W pcap (2026-04-08).

### 2.4 Poll (host → radio) — 3 bytes

```
02 02 FF
```

The radio **requires a poll every ~1 second** or it stops sending DV data. Polls must continue even while receiving or transmitting voice.

### 2.5 Poll Ack (radio → host) — 4 bytes

```
03 03 <status> FF
```

| Status | Meaning |
|--------|---------|
| 0x00   | Idle / normal |
| 0x01   | Ready for TX voice (seen immediately after TX header ack) |

### 2.6 RX Header (radio → host) — 45 bytes

The radio sends this when it decodes a D-STAR DV header from the air.

```
[LEN=44][0x10][FLAG1][FLAG2][FLAG3][RPT2(8)][RPT1(8)][URCALL(8)][MYCALL(8)][MYCALL2(4)][CRC_LO][CRC_HI][0x00][0xFF]
```

The payload is 42 bytes: a complete 41-byte D-STAR header (including CRC) followed by one reserved `0x00` byte. The CRC is present and valid; see §3.4.

### 2.7 RX Voice (radio → host) — 17 bytes

```
[LEN=16][0x12][seq1][seq2][AMBE(9)][SlowData(3)][0xFF]
```

| Field     | Description |
|-----------|-------------|
| seq1      | Absolute frame counter (increments monotonically across a full transmission) |
| seq2      | D-STAR sequence number 0–20 (lower 6 bits); bit 0x40 set on the last frame of a transmission |
| AMBE[9]   | AMBE+2 compressed voice |
| SlowData[3] | Slow-data bytes (scrambled; see §3.3) |

Voice frames arrive every ~20 ms (50 fps). A transmission ends when a frame arrives with `seq2 & 0x40 != 0`.

### 2.8 TX Header (host → radio) — 42 bytes

Send this before transmitting voice. The radio generates its own CRC; do **not** include the 2-byte CRC in this frame.

```
[LEN=41][0x20][FLAG1=0x01][FLAG2][FLAG3][RPT2(8)][RPT1(8)][URCALL(8)][MYCALL(8)][MYCALL2(4)][0xFF]
```

FLAG1 **must be 0x01** to signal the TX direction to the radio. FLAG2 and FLAG3 are normally 0x00.

Total data = 3 flag bytes + 4×8 callsign bytes + 4 suffix bytes = 39 bytes.

### 2.9 TX Header Ack (radio → host) — 4 bytes

```
03 21 00 FF
```

The radio sends this after accepting the TX header. Immediately after, a Poll Ack with status=0x01 is sent to indicate readiness for voice frames.

### 2.10 TX Voice (host → radio) — 17 bytes

```
[LEN=16][0x22][seq1][seq2][AMBE(9)][SlowData(3)][0xFF]
```

| Field     | Description |
|-----------|-------------|
| seq1      | Absolute TX frame counter; reset to 0 on each new TX header, incremented by caller |
| seq2      | D-STAR sequence 0–20 (lower 6 bits); set bit 0x40 on the last frame |
| AMBE[9]   | AMBE+2 voice; use `SilenceAMBE = {9E 8D 32 88 26 1A 3F 61 E8}` when no audio |
| SlowData[3] | Slow-data bytes (scrambled; see §3.3) |

The last frame of a transmission has both seq2 bit 0x40 set and the AMBE/SlowData fields filled with the end-of-stream marker (`55 C8 7A` AMBE, `55 55 55` slow data — as seen in Doozy pcap).

### 2.11 TX Voice Ack (radio → host) — 5 bytes

```
04 23 <seq1> 00 FF
```

The radio echoes `seq1` from the corresponding TX voice frame. Acks may be received slightly out-of-phase with transmissions; they can be ignored for basic operation.

### 2.12 Timing Summary

| Event | Interval |
|-------|----------|
| Poll  | Every ~1 s (required; radio stops responding without it) |
| Voice frame RX/TX | Every 20 ms (50 fps) |
| TX Header → first TX Voice | Send voice immediately after TX header ack |

---

## 3. D-STAR Frame Formats

### 3.1 DV Header — 41 bytes

```
[FLAG1][FLAG2][FLAG3][RPT2(8)][RPT1(8)][URCALL(8)][MYCALL(8)][MYCALL2(4)][CRC_LO][CRC_HI]
```

| Field    | Size | Description |
|----------|------|-------------|
| FLAG1    | 1    | 0x00 for received, 0x01 for TX (gateway direction bit) |
| FLAG2    | 1    | Usually 0x00 |
| FLAG3    | 1    | Usually 0x00 |
| RPT2     | 8    | Outbound repeater/reflector callsign, space-padded |
| RPT1     | 8    | Local repeater callsign, space-padded |
| URCALL   | 8    | Destination callsign (`"CQCQCQ  "` for CQ call) |
| MYCALL   | 8    | Source callsign, space-padded |
| MYCALL2  | 4    | 4-char callsign suffix (e.g. `"705 "`, `"    "` if none) |
| CRC      | 2    | CRC-16, little-endian (see §3.4) |

All callsign fields are **right-padded with spaces** to their fixed width.

### 3.2 DV Voice Frame — 12 bytes

Each frame carries 9 bytes of AMBE+2 compressed audio and 3 bytes of slow data. On the serial interface an additional seq1 and seq2 byte precede these (totalling 14 bytes of payload; see §2.6 and §2.9).

**Silence AMBE:** `9E 8D 32 88 26 1A 3F 61 E8` — use this to fill frames when audio is unavailable.

**End-of-stream AMBE:** `55 C8 7A 55 55 55 55 55 55` (as observed in Doozy pcap last TX frames).

### 3.3 Slow Data

Three bytes of slow data are carried in every voice frame. Over 20 frames (one "superframe"), 60 bytes accumulate. Each byte is XOR'd with a scrambling sequence before transmission.

**Scrambler table** (20 bytes, indexed by `seq % 20`):
```
70 4F 93 40 64 74 6D 30 2B 2B BE CC 9E 50 00 7F D5 97 D7 22
```

Each 3-byte slow-data field uses positions `[seq%20]`, `[(seq+1)%20]`, `[(seq+2)%20]` of the table. Scrambling is self-inverse (same XOR for encode and decode).

**Slow data types** (first unscrambled byte of a block):
| Type | Meaning |
|------|---------|
| 0x30 | GPS/DPRS |
| 0x43 | Short text message |
| 0x00 | Null/filler |

**Sending null slow data:** XOR `{0x00, 0x00, 0x00}` with the scrambler for the given seq.

### 3.4 D-STAR Header CRC

> **Corrected 2026-04-01.** Verified against Doozy pcap. The prior implementation (MSB-first) was wrong and caused all received headers to fail validation.

The CRC covers the first 39 bytes of the header (flags + callsigns, not the CRC field itself). It is stored **little-endian** in bytes 39–40.

**Algorithm:** LSB-first reflected CRC-CCITT
- Polynomial: 0x8408 (reflected form of 0x1021)
- Initial value: 0xFFFF
- Final XOR: 0xFFFF (result is inverted before storage)

```go
func crc16CCITT(data []byte) uint16 {
    crc := uint16(0xFFFF)
    for _, b := range data {
        crc ^= uint16(b)
        for i := 0; i < 8; i++ {
            if crc&1 != 0 {
                crc = (crc >> 1) ^ 0x8408
            } else {
                crc >>= 1
            }
        }
    }
    return ^crc
}
```

**Test vector (from Doozy pcap):**
- Input: `00 00 00` + `"DIRECT  DIRECT  CQCQCQ  KR4GCQ  705 "` (39 bytes)
- CRC: `0x2266` → stored as `66 22`

---

## 4. DPlus Protocol (REF Reflectors)

DPlus is used by REF reflectors (e.g. `ref001.dstargateway.org`). Transport: **UDP port 20001**.

### 4.1 Connection Handshake

**Step 1 — Echo test:**
```
Host → Server:  05 00 18 00 01   (CT_LINK1)
Server → Host:  05 00 18 00 01   (echo back)
```

**Step 2 — Login:**
```
Host → Server:  1C C0 04 00 [callsign, null-padded to 16 bytes] [DV019999]
                              ^bytes 4–19                          ^bytes 20–27
Server → Host:  08 C0 04 00 4F 4B 52 57   ("OKRW" = accepted)
             or 08 C0 04 00 42 55 53 59   ("BUSY" = module in use)
```

**Keepalive:** `03 60 00` sent every 5 seconds in both directions.

**Disconnect:** Send `05 00 18 00 00` (CT_LINK1 with byte[4]=0x00) twice.

### 4.2 DSVT Data Packets

All DPlus packets use a 2-byte prefix: byte 0 = total packet length, byte 1 = class marker (0x80 for DSVT data, 0xC0 for control, 0x60 for keepalive).

DV data is wrapped in DSVT frames:

```
[len][0x80][D][S][V][T][type][0x00][0x00][0x00][streamID(2)][seq/flags][payload...]
```

**Header packet (58 bytes):**
```
[3A][80][DSVT][10][00 00 00][streamID(2)][80][41-byte D-STAR header][00 00 00 00]
```

**Voice packet (29 bytes):**
```
[1D][80][DSVT][20][00 00 00][streamID(2)][seq][AMBE(9)][SlowData(3)][00 00 00 00]
```

> **Corrected 2026-04-08:** Bytes 0-1 are NOT an LE uint16 length. Byte 0 is single-byte total length, byte 1 is 0x80 data marker. Stream ID is 2 bytes. Bytes 7-9 are 0x00 (not 0x03). Verified against G4KLX ircddbGateway DPlusHandler source.

`seq` uses the same convention as the serial protocol: lower 6 bits = D-STAR sequence 0–20, bit 0x40 set on last frame.

### 4.3 Local Port Note

REF reflectors expect packets to originate from UDP port 20001. The client attempts to bind port 20001 locally; if it is already in use it falls back to an ephemeral port (some reflectors may reject this).

---

## 5. ircDDB Gateway Registration

Connects to `openquad.net:9007` (TCP) using a standard IRC handshake.

**Nick format:** `<lowercase_callsign>-<module_number>`
- Module letter maps to 1-based number: A=1, B=2, C=3, D=4, …
- Example: `KR4GCQ D` → nick `kr4gcq-4`

**Announcing a transmission:** On each TX header event, send:
```
PRIVMSG #dstar :@U <mycall> <rpt1> <rpt2> <urcall>
```
This registers the callsign in the ircDDB routing table and makes it appear as "heard" on reflector status pages.

---

## 6. DExtra Protocol (XRF Reflectors)

XRF reflectors use the DExtra protocol. Implementation is in `internal/protocol/dextra/`. The framing is similar to DPlus but uses a different handshake and packet structure. (Detail to be expanded as testing is performed.)

---

## 7. XLX Protocol

XLX reflectors are multi-protocol. The XLX client in `internal/protocol/xlx/` implements the relevant subset. (Detail to be expanded as testing is performed.)

---

## 8. Pcap Evidence Summary

### `doozy-cap.pcapng` observations (2026-04-01)

| Observation | Details |
|-------------|---------|
| USB device | Prolific PL2303 (`0x0c26:0x0036`), CDC-ACM, endpoints 0x04 OUT / 0x85 IN |
| Setup | Only GET DESCRIPTOR + SET CONFIGURATION; no CDC line coding sent |
| First poll sent | t = 0.880 s after capture start |
| Poll ack latency | ~0.8 ms |
| First RX header | t = 0.935 s (55 ms after first poll) |
| Voice frame interval | Exactly 20 ms (50 fps) |
| TX sequence observed | TX header → TX header ack + poll ack (status=1) → 8 TX voice frames → end frame |
| CRC verification | Header `DIRECT/DIRECT/CQCQCQ/KR4GCQ/705` → CRC = `66 22` ✓ |

### `rsms3w.pcapng` observations (2026-04-08)

| Observation | Details |
|-------------|---------|
| USB device | IC-705 (VID `0x0c26` PID `0x0036`), CDC-ACM, endpoints 0x04 OUT / 0x85 IN |
| CDC setup | GET_LINE_CODING → 38400 8N1; SET_LINE_CODING(38400 8N1); SET_CONTROL_LINE_STATE(0) — on both interfaces 0 and 2 |
| Init flush | First data sent on DV endpoint: `FF FF FF`; radio echoes back `FF FF FF` |
| First poll | `02 02 FF` sent immediately after init flush |
| First poll ack | `03 03 00 FF` (4 bytes, proper response) |
| Poll interval | ~1 s between polls |
| RX header | 45 bytes: DIRECT/DIRECT//URF621A/KR4GCQ/705 — active D-STAR reflector connection |
| RX voice | 17-byte frames every 20 ms |
| TX header FLAG1 | 0x02 (RS-MS3W uses FLAG1=0x02, Doozy uses FLAG1=0x01 — both work) |
| TX voice | 9 frames (seq 0–8), silence AMBE, last frame has end flag + `55 C8 7A` AMBE |
| Baud rate | **38400** (not 115200) — this is the key difference from prior failed macOS tests |
