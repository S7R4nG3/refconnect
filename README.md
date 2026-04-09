# RefConnect

A D-STAR reflector client for macOS, Windows, and Linux that connects your D-STAR radio to internet-based reflectors via serial port. Supports DExtra (XRF), DPlus (REF), and XLX reflector protocols.

![RefConnect Screenshot](docs/screenshot.png)

---

## Features

- Multi-OS Support! (Linux/MacOS/Windows)
- Connect to XRF, REF, and XLX reflectors
- Serial port integration with D-STAR radios
- Saved reflector profiles with last-used memory
- Dark, light, or system theme support
- Timestamped activity log

TODO:

- DCS, XRF, XLX reflector support (REF is working via DPlus)
- Simple shell install script
- Simple user guide
- Troubleshooting steps + support

## Requirements

- macOS, Windows, or Linux
- A D-STAR capable radio connected via serial port

## Tested Radios

- ICOM IC-705

## Building

Requires make, git, and go 1.22+

```bash
git clone https://github.com/S7R4nG3/refconnect.git
cd refconnect/

# Build binary
make build
```

## Configuration

On first launch, a default configuration is created at:

```
~/.config/refconnect/config.yaml
```

Key settings:

```yaml
version: 1
callsign: N0CALL
callsign_suffix: ' '
radio:
    port: /dev/cu.usbmodem1203
    baud_rate: 38400
    data_bits: 8
    stop_bits: 1
    parity: "N"
    ptt_via_rts: false
reflectors:
    - name: REF001 C
      host: ref001.dstargateway.org
      port: 20001
      module: C
      protocol: DPlus
last_used_reflector: "REF001 C"
ui:
    theme: system
    log_max_lines: 500
    window_width: 960
    window_height: 720
```

Reflector profiles can be added to the `reflectors` list and selected from the Connect panel on launch.

## Usage

1. **Select a reflector** — Choose the type (XRF/REF/XLX), enter the reflector ID and domain, and select a module (A–Z).
2. **Enter your callsign** — Set your callsign and gateway module suffix in the Connect panel.
3. **Click Connect** — The status panel will update once the link is established.
4. **Open your radio** — Select the serial port from the PTT panel and click **Open**.
5. **Transmit** — Key up on your radio! Welcome to DStar!

The log panel shows timestamped activity including connections, heard callsigns, and errors.

## License

See [LICENSE](LICENSE).

©️ 2026 Dave Streng (KR4GCQ)