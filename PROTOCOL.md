# RefConnect Protocol Reference

This document captures everything known about the protocols used by RefConnect, derived from source code, pcap analysis, and the D-STAR specification.

---

## 1. Hardware Connection — Icom IC-705

The IC-705 USB-B uses an internal Prolific PL2303 chip (VID/PID `0x0c26:0x0036`). The DV Gateway Terminal protocol works over **USB-B** — no external adapter is needed.

```
IC-705 ──► USB-B cable ──► Host USB
```

**Serial parameters:** **38400 baud**, 8 data bits, no parity, 1 stop bit (8N1). A pcap confirms SET_LINE_CODING(38400) on the DV CDC interface. Although baud rate is nominally virtual on USB-CDC, the IC-705 firmware may use the SET_LINE_CODING value to select the DV Gateway Terminal mode — using 115200 or 9600 on macOS results in no response (only `FF` bytes returned).

### USB interface layout

The IC-705 presents as a single USB device with **two CDC-ACM virtual serial ports** (two IAD groups):

| Port | Interfaces | Endpoints | Function |
|------|-----------|-----------|----------|
| First  | 0 (CDC Control) + 1 (CDC Data) | 0x01 OUT / 0x02 IN | CI-V control |
| Second | 2 (CDC Control) + 3 (CDC Data) | 0x04 OUT / 0x85 IN | **DV Gateway Terminal data** |

On macOS, the two ports appear as `/dev/cu.usbmodem*1` and `/dev/cu.usbmodem*3` (the suffix digit matches the CDC Data interface number). The **`*3` port is the DV data port**.

**CDC setup required:** A pcap shows SET_LINE_CODING(38400, 8N1) and SET_CONTROL_LINE_STATE(0) are sent before data transfer begins. On macOS, these are issued by the kernel CDC-ACM driver when the serial port is opened with the corresponding baud rate.

**Init flush:** Before sending the first poll, RS-MS3W sends `FF FF FF` (3 terminator bytes) on the DV data endpoint. The radio echoes back `FF FF FF`. This clears any partial frame state in the radio's protocol parser. Without this init, macOS gets only single `FF` bytes in response to polls.

**IC-705 radio settings:**
- MODE button → select **DV** (not FM/SSB/AM)
- `MENU > SET > CONNECTORS > ACC/USB OUTPUT SELECT` → **DV Data**

---

## 2. DV Gateway Terminal Serial Protocol

This is the binary framing protocol spoken over the serial link between the host and the IC-705. It was reverse-engineered from USBPcap captures and verified in full against another pcap.

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

Sent once before the first poll. Clears any residual parser state in the radio. The radio echoes back `FF FF FF`. Required on macOS; observed in pcap (2026-04-08).

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

The last frame of a transmission has both seq2 bit 0x40 set and the AMBE/SlowData fields filled with the end-of-stream marker (`55 C8 7A` AMBE, `55 55 55` slow data — as seen in pcap).

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

**End-of-stream AMBE:** `55 C8 7A 55 55 55 55 55 55` (as observed in pcap last TX frames).

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

> **Corrected 2026-04-01.** Verified against pcap. The prior implementation (MSB-first) was wrong and caused all received headers to fail validation.

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

**Test vector (from pcap):**
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

## 8. MMDVM Protocol (Kenwood TH-D75)

The TH-D75 implements an MMDVM-compatible modem interface, communicating over Bluetooth SPP (Serial Port Profile) or USB serial. Unlike the IC-705's proprietary DV Gateway Terminal protocol, the TH-D75 speaks the standard MMDVM serial protocol as defined by the g4klx/MMDVM firmware project. The canonical host-side reference implementation is g4klx/MMDVMHost; DroidStar (doug-h/DroidStar) is another client known to work with the TH-D75.

### 8.1 Hardware Connection

```
TH-D75 ──► Bluetooth SPP ──► Host (virtual serial port)
  or
TH-D75 ──► USB cable ──► Host USB (CDC-ACM virtual serial port)
```

**USB device:** VID `0x2166` PID `0x9023`. Presents as a composite USB device with 4 interfaces:
- Interface 0 (CDC Control) + Interface 1 (CDC Data) — virtual serial port (endpoints 0x01 OUT, 0x81 IN)
- Interface 2 (Audio Control) + Interface 3 (Audio Streaming) — USB audio (endpoint 0x83 IN, 48kHz 16-bit mono PCM)

Only **one** CDC serial port (unlike the IC-705 which has two).

**Serial parameters:** **115200 baud**, 8 data bits, no parity, 1 stop bit (8N1). Confirmed by BlueDV pcap CDC SET_LINE_CODING (`00 C2 01 00 00 00 08` = 115200, 1 stop, no parity, 8 bits).

**Radio settings (TH-D75):**
- Menu 650: **Reflector TERM Mode** — must be enabled
- Menu 985: **Bluetooth** — must be set if connecting via BT SPP

**Port initialization:**
- Set **DTR high** — required for TH-D75 (confirmed by BlueDV docs)
- Set **RTS high** — DroidStar does this
- Wait **2 seconds** after opening the port before sending any commands (per MMDVMHost)
- Drain any bytes the modem sent during the init delay

No special init-flush sequence (unlike the IC-705's `FF FF FF`). Just open, wait, then start the handshake.

### 8.2 Frame Structure

Every MMDVM frame follows this envelope:

```
[0xE0][LENGTH][COMMAND][PAYLOAD...]
```

| Field   | Size     | Description |
|---------|----------|-------------|
| 0xE0    | 1 byte   | Frame start marker (always `0xE0`) |
| LENGTH  | 1 byte   | Total frame length including start, length, command, and payload bytes |
| COMMAND | 1 byte   | Command/type identifier |
| PAYLOAD | variable | Command-specific data (may be empty) |

**No terminator byte.** The length field tells you when the frame ends. This differs from the IC-705 DV Gateway Terminal protocol which uses `0xFF` as a terminator.

Maximum frame size: 255 bytes.

When reading, scan for the `0xE0` start byte, discarding any garbage/noise bytes until found.

### 8.3 Command Types

| Constant | Value | Direction | Description |
|----------|-------|-----------|-------------|
| GET_VERSION | 0x00 | host → modem | Request firmware version and protocol info |
| GET_STATUS | 0x01 | host → modem | Request modem status (keepalive) |
| SET_CONFIG | 0x02 | host → modem | Configure modem modes and levels |
| SET_MODE | 0x03 | host → modem | Set operating mode (0x00=idle, 0x01=D-STAR) |
| SET_FREQ | 0x04 | host → modem | Set RX/TX frequency (modem ACKs but ignores values) |
| DSTAR_HEADER | 0x10 | bidirectional | D-STAR header (41 bytes) |
| DSTAR_DATA | 0x11 | bidirectional | D-STAR voice frame (12 bytes: 9 AMBE + 3 slow data) |
| DSTAR_LOST | 0x12 | modem → host | D-STAR signal lost |
| DSTAR_EOT | 0x13 | bidirectional | D-STAR end of transmission |
| ACK | 0x70 | modem → host | Command accepted |
| NAK | 0x7F | modem → host | Command rejected (payload[1] = reason code) |

### 8.4 Handshake Sequence

The handshake must complete before the modem will accept D-STAR data. The following sequence was captured from a BlueDV ↔ TH-D75 USB pcap (2026-04-14):

```
1. Open port, set DTR/RTS high
2. Sleep 2 seconds (modem init time)
3. Drain any buffered bytes
4. GET_VERSION     →  wait for version response
5. SET_MODE(idle)  →  wait for ACK
6. SET_FREQ        →  wait for ACK
7. SET_CONFIG      →  wait for ACK
8. SET_MODE(D-STAR)→  wait for ACK     ← activates D-STAR mode
9. Start GET_STATUS polling every 250ms
```

> **Important:** SET_MODE(D-STAR) after SET_CONFIG is required. Without it, the TH-D75 will not process D-STAR data frames. BlueDV also sends SET_MODE(idle) after EOT to deactivate D-STAR mode.

#### GET_VERSION (3 bytes)

```
Host → Modem:  E0 03 00
```

**Response (variable length):**

Protocol v1 (TH-D75 reports this):
```
Modem → Host:  E0 <len> 00 <version=1> <description...>
```

Protocol v2:
```
Modem → Host:  E0 <len> 00 <version=2> <capabilities1> <capabilities2> <cpuType> <UDID(16)> <description...>
```

The version byte at payload[0] determines which SET_CONFIG layout to use. The TH-D75 reports **protocol version 1** with description `TH-D75 RTM1.00` (full response: `E0 12 00 01 54 48 2D 44 37 35 20 52 54 4D 31 2E 30 30`).

**Retry logic:** MMDVMHost retries GET_VERSION up to **6 times** with **1.5-second gaps** between attempts. Each attempt polls for up to 30 response frames. If no version response after all retries, the handshake fails.

#### SET_MODE (4 bytes)

```
Host → Modem:  E0 04 03 <mode>
```

| Mode | Meaning |
|------|---------|
| 0x00 | Idle — no mode active |
| 0x01 | D-STAR active |

BlueDV sends SET_MODE(idle) before SET_FREQ during init, SET_MODE(D-STAR) after SET_CONFIG to activate, and SET_MODE(idle) after DSTAR_EOT to deactivate.

**Expected response:** `ACK` (0x70) — the ACK payload echoes cmd 0x03: `E0 04 70 03`

#### SET_FREQ (12 bytes)

```
Host → Modem:  E0 0C 04 <9 payload bytes>
```

Payload layout (9 bytes):
```
[0]     = 0x00                  (padding)
[1..4]  = RX frequency (Hz), little-endian uint32
[5..8]  = TX frequency (Hz), little-endian uint32
```

> **Note (2026-04-14):** BlueDV pcap confirms only 9 payload bytes — no rfLevel or POCSAG frequency fields (unlike MMDVMHost which sends 14). The firmware ACKs SET_FREQ without processing the values. BlueDV sends 434.3 MHz (`60 E4 E2 19` LE = 434300000 Hz).

**Expected response:** `ACK` (0x70)

#### SET_CONFIG — Protocol v1 (21 bytes) — TH-D75

The TH-D75 reports protocol v1. This is the layout confirmed by BlueDV pcap.

```
Host → Modem:  E0 15 02 <18 payload bytes>
```

**Exact bytes from pcap:** `E0 15 02 82 01 0A 00 80 80 01 00 80 7E 7E 7E 7E 80 80 80 04 80`

v1 payload layout (18 bytes):

| Index | Field | Value | Notes |
|-------|-------|-------|-------|
| 0 | flags | 0x82 | simplex(0x80) + txInvert(0x02) |
| 1 | modes | 0x01 | D-STAR enabled |
| 2 | txDelay | 0x0A | 10 × 10ms = 100ms |
| 3 | modemState | 0x00 | MODE_IDLE |
| 4 | rxLevel | 0x80 | 128 (~50%) |
| 5 | cwIdTXLevel | 0x80 | 128 |
| 6 | dmrColorCode | 0x01 | |
| 7 | dmrDelay | 0x00 | |
| 8 | oscOffset | 0x80 | 128 (0 ppm) |
| 9 | dstarTXLevel | 0x7E | 126 |
| 10 | dmrTXLevel | 0x7E | 126 |
| 11 | ysfTXLevel | 0x7E | 126 |
| 12 | p25TXLevel | 0x7E | 126 |
| 13 | txDCOffset | 0x80 | 128 (0 offset) |
| 14 | rxDCOffset | 0x80 | 128 (0 offset) |
| 15 | nxdnTXLevel | 0x80 | 128 |
| 16 | ysfTXHang | 0x04 | |
| 17 | p25TXHang | 0x80 | |

> **Note:** The flags byte **must include txInvert (0x02)**. BlueDV sends 0x82 (simplex + txInvert). Without txInvert, the modem may not communicate correctly.

> **Note:** BlueDV sends only 18 payload bytes (21 total) vs MMDVMHost's 23 payload bytes (26 total). The firmware accepts the shorter payload.

**Expected response:** `ACK` (0x70) if accepted, `NAK` (0x7F) with reason byte if rejected.

#### SET_CONFIG — Protocol v2 (40 bytes)

For modems reporting protocol v2. Layout matches MMDVMHost `setConfig2`.

```
Host → Modem:  E0 28 02 <37 payload bytes>
```

v2 payload layout (37 bytes):

| Index | Field | Value | Notes |
|-------|-------|-------|-------|
| 0 | flags | 0x82 | simplex(0x80) + txInvert(0x02) |
| 1 | modes1 | 0x01 | D-STAR enabled |
| 2 | modes2 | 0x00 | POCSAG enable (not used) |
| 3 | txDelay | 0x0A | TX delay in 10ms units |
| 4 | modemState | 0x00 | MODE_IDLE |
| 5 | txDCOffset | 128 | DC offset + 128 (0 = centered) |
| 6 | rxDCOffset | 128 | DC offset + 128 (0 = centered) |
| 7 | rxLevel | 128 | RX level (~50%) |
| 8 | cwIdTXLevel | 128 | CW ID TX level |
| 9 | dstarTXLevel | 128 | D-STAR TX level |
| 10–13 | other TX levels | 0 | DMR, YSF, P25, NXDN (unused) |
| 14–36 | reserved | 0 | pocsagTXLevel, hang times, etc. |

### 8.5 ACK / NAK Frames

**ACK:**
```
Modem → Host:  E0 04 70 <cmd>
```
`cmd` echoes the command byte that was accepted.

**NAK:**
```
Modem → Host:  E0 05 7F <cmd> <reason>
```

| Reason | Meaning |
|--------|---------|
| 1 | Invalid command |
| 2 | Wrong mode for this command |
| 3 | Command too short |
| 4 | Invalid configuration data |
| 5 | Invalid D-STAR data length |

### 8.6 GET_STATUS (Keepalive Polling)

```
Host → Modem:  E0 03 01
```

Sent every **250ms** (matching MMDVMHost). This serves as a keepalive and also provides modem state.

**Response:**
```
Modem → Host:  E0 <len> 01 <enabledModes> <modemState> <byte3> <dstarSpace> ...
```

| Field | Offset | Description |
|-------|--------|-------------|
| enabledModes | payload[0] | Bitmask of enabled modes (bit 0 = D-STAR) |
| modemState | payload[1] | Current operating state (0 = idle) |
| dstarSpace | payload[3] | Number of D-STAR frames the modem can buffer |

### 8.7 D-STAR Data Frames

#### DSTAR_HEADER (0x10) — 44 bytes total

```
Host → Modem:  E0 2C 10 [41-byte D-STAR header with CRC]
Modem → Host:  E0 2C 10 [41-byte D-STAR header with CRC]
```

The payload is a standard 41-byte D-STAR header (see §3.1): flags(3) + callsigns(32) + CRC(2) + padding. Both directions use the same format. CRC is included (unlike the IC-705 TX header which omits CRC).

#### DSTAR_DATA (0x11) — 15 bytes total

```
Host → Modem:  E0 0F 11 [AMBE(9)][SlowData(3)]
Modem → Host:  E0 0F 11 [AMBE(9)][SlowData(3)]
```

12 bytes of payload: 9 bytes AMBE+2 voice + 3 bytes slow data. No sequence numbers in the MMDVM frame itself (unlike the IC-705 protocol which carries seq1/seq2 bytes). The host is responsible for tracking sequence.

#### DSTAR_EOT (0x13) — 15 bytes total

```
Host → Modem:  E0 0F 13 [end pattern(12)]
Modem → Host:  E0 0F 13 [end pattern(12)]
```

End-of-transmission pattern (12 bytes):
```
55 55 55 55 55 55 55 55 55 55 C8 7A
```

#### DSTAR_LOST (0x12) — 3 bytes total

```
Modem → Host:  E0 03 12
```

Modem-to-host only. Indicates the D-STAR signal was lost (RF timeout). The host should treat this the same as EOT.

### 8.8 Key Differences: MMDVM vs DV Gateway Terminal

| Aspect | IC-705 (DV Gateway Terminal) | TH-D75 (MMDVM) |
|--------|------------------------------|-----------------|
| Start marker | None (length byte is first) | `0xE0` |
| Terminator | `0xFF` always last byte | None (length-delimited) |
| Length field | `total_bytes - 1` | `total_bytes` (inclusive) |
| Baud rate | 38400 | 115200 |
| Init sequence | `FF FF FF` flush required | 2s sleep, then GET_VERSION handshake |
| Keepalive | Poll (`02 02 FF`) every ~1s | GET_STATUS every 250ms |
| TX header CRC | Radio generates CRC (don't send it) | CRC included in frame |
| Voice frame seq | seq1 + seq2 bytes in frame | No sequence in frame (host tracks) |
| Handshake | None (just start polling) | GET_VERSION → SET_MODE → SET_FREQ → SET_CONFIG → SET_MODE(D-STAR) |
| PTT control | Via data flow or RTS | Via data flow (header starts TX, EOT ends it) |

### 8.9 Reference Implementations

- **g4klx/MMDVMHost** (`Modem.cpp`) — canonical host-side implementation. The `open()` method defines the handshake order. `setConfig1()`/`setConfig2()` define SET_CONFIG layouts for v1/v2.
- **g4klx/MMDVM** (`SerialPort.cpp`) — canonical firmware. The `setConfig()` function parses SET_CONFIG payloads and validates lengths.
- **doug-h/DroidStar** — known working client for TH-D75 over Bluetooth SPP.
- **BlueDV** (Windows) — confirmed working with TH-D75 over USB-C; pcap captured 2026-04-14.

---

## 9. Pcap Evidence Summary

### Observations (2026-04-01)

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

### Observations (2026-04-08)

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

### Observations (2026-04-14) — BlueDV ↔ TH-D75 USB-C

| Observation | Details |
|-------------|---------|
| USB device | TH-D75 (VID `0x2166` PID `0x9023`), composite: CDC-ACM (interfaces 0+1) + USB Audio (interfaces 2+3) |
| CDC endpoints | 0x01 OUT / 0x81 IN (bulk), 0x82 IN (interrupt) — single serial port |
| Audio endpoint | 0x83 IN (isochronous, 48kHz 16-bit mono PCM) |
| CDC setup | GET_LINE_CODING → SET_LINE_CODING(115200 8N1) — baud confirmed at 115200 |
| Firmware version | Protocol v1, description `TH-D75 RTM1.00` |
| Handshake timing | Port opened at t≈4.0s; version response at t=4.41s; handshake complete at t=5.04s |
| Handshake sequence | GET_VERSION → SET_MODE(idle) → SET_FREQ(434.3MHz) → SET_CONFIG(v1, flags=0x82) → SET_MODE(D-STAR) |
| SET_CONFIG flags | 0x82 = simplex(0x80) + txInvert(0x02) — txInvert is required |
| SET_CONFIG size | 18 payload bytes (21 total), shorter than MMDVMHost's 23 payload (26 total) |
| SET_FREQ size | 9 payload bytes (12 total) — no rfLevel or POCSAG fields (shorter than MMDVMHost's 14 payload) |
| SET_MODE usage | Sent idle before SET_FREQ, D-STAR after SET_CONFIG, idle after EOT |
| Status response | `E0 0A 01 01 01 00 7F 00 00 00` — enabledModes=0x01 (D-STAR), dstarSpace=127 |
| Status polling | GET_STATUS sent every ~50-60ms during handshake, then interleaved with data during TX |
| D-STAR TX header | 44 bytes: RPT2=`KR4GCQ C` RPT1=`KR4GCQ G` URCALL=`CQCQCQ  ` MYCALL=`KR4GCQ  ` MYCALL2=`Blue` |
| D-STAR TX voice | 15-byte frames every ~5ms during TX burst |
| D-STAR EOT | `E0 03 13` sent at end of transmission |
| Post-EOT | SET_MODE(idle) sent ~5.5s after EOT |
| No RX data seen | Pcap only captured TX (host → radio) D-STAR data; no incoming RF during capture |
