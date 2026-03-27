# RefConnect

A D-STAR reflector client for macOS that connects your D-STAR radio to internet-based reflectors via serial port. Supports DExtra (XRF), DPlus (REF), and XLX reflector protocols.

![RefConnect Screenshot](docs/screenshot.png)

---

## Features

- Connect to XRF, REF, and XLX reflectors
- Serial port integration with D-STAR radios
- PTT control via RTS line or on-screen button (spacebar shortcut)
- Automatic callsign registration via ircDDB
- Saved reflector profiles with last-used memory
- Dark, light, or system theme support
- Timestamped activity log

## Requirements

- macOS
- Go 1.22+
- A D-STAR capable radio connected via serial port

## Building

```bash
# Build binary
make build

# Build and bundle as a macOS .app
make bundle

# Build, bundle, and launch
make run
```

## Configuration

On first launch, a default configuration is created at:

```
~/.config/refconnect/config.yaml
```

Key settings:

```yaml
callsign: "N0CALL  "       # Your amateur radio callsign (8 chars, space-padded)
radio:
  port: "/dev/ttyUSB0"     # Serial port for your radio
  baud_rate: 115200
  ptt_via_rts: true        # PTT via RTS serial line
ui:
  theme: "system"          # dark, light, or system
```

Reflector profiles can be added to the `reflectors` list and selected from the Connect panel on launch.

## Usage

1. **Select a reflector** — Choose the type (XRF/REF/XLX), enter the reflector ID and domain, and select a module (A–Z).
2. **Enter your callsign** — Set your callsign and gateway module suffix in the Connect panel.
3. **Click Connect** — The status panel will update once the link is established.
4. **Open your radio** — Select the serial port from the PTT panel and click **Open**.
5. **Transmit** — Press the PTT button or tap the spacebar to key up. Release to unkey.

The log panel shows timestamped activity including connections, heard callsigns, and errors.

## License

See [LICENSE](LICENSE).
