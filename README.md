[![Latest Release](https://img.shields.io/github/v/release/S7R4nG3/refconnect?label=Latest%20Release)](https://github.com/S7R4nG3/refconnect/releases/latest)   [![Dependabot Updates](https://github.com/S7R4nG3/refconnect/actions/workflows/dependabot/dependabot-updates/badge.svg)](https://github.com/S7R4nG3/refconnect/actions/workflows/dependabot/dependabot-updates)     [![Build Status](https://github.com/S7R4nG3/refconnect/actions/workflows/ci.yml/badge.svg)](https://github.com/S7R4nG3/refconnect/actions/workflows/ci.yml)

# RefConnect

A D-STAR reflector client for macOS, Windows, and Linux that connects your D-STAR radio to internet-based reflectors via serial port. Supports DExtra (XRF), DPlus (REF), XLX, and DCS reflector protocols.

![RefConnect Screenshot](docs/screenshot.png)

--- 

## Features

- Multi-OS Support! (Linux/MacOS/Windows)
- Connect to REF/XRF/XLX/DCS reflectors via DPlus/DExtra/XLX protocols
- Supports ICOM (DV Gateway Terminal) and Kenwood (MMDVM) radio protocols
- Serial port integration with D-STAR radios via USB
- Saved reflector profiles with last-used memory
- Dark, light, or system theme support
- Timestamped activity log

## Simple Setup

MacOs & Linux:

```shell
curl -fsSL https://raw.githubusercontent.com/S7R4nG3/refconnect/main/configs/setup.sh | sh
```

Windows:

```powershell
iwr -useb https://raw.githubusercontent.com/S7R4nG3/refconnect/main/configs/setup.ps1 | iex
```

Check out the [Wiki](https://github.com/S7R4nG3/refconnect/wiki) for more information!


## Configuration

On first launch, a default configuration is created at:

```
~/.config/refconnect/config.yaml
```

Key settings:

```yaml
version: 1
callsign: N0CALL
callsign_suffix: G
radio:
    port: /dev/cu.usbmodem1203
    protocol: DV-GW              # "DV-GW" (ICOM) or "MMDVM" (Kenwood)
reflectors:
    - name: REF001 C
      host: ref001.dstargateway.org
      port: 20001
      module: C
      protocol: DPlus
last_used_reflector: "REF001 C"
ui:
    theme: system                # "dark", "light", or "system"
    log_max_lines: 500
    window_width: 960
    window_height: 720
```

| Field | Description |
|---|---|
| `callsign` | Your amateur radio callsign |
| `callsign_suffix` | Gateway module suffix (e.g. `G` for gateway). This is the 8th character of your callsign as shown in Via/Peer on reflector dashboards |
| `radio.port` | Serial port path for your radio |
| `radio.protocol` | `DV-GW` for ICOM radios, `MMDVM` for Kenwood radios. Baud rate is set automatically (38400 / 115200) |
| `reflectors` | Saved reflector profiles, selectable from the Connect panel |
| `ui.theme` | `dark`, `light`, or `system` |

## Usage

1. **Connect your radio** — Plug in your radio and turn it on. RefConnect detects supported radios, selects the right serial port and sets the protocol (ICOM or Kenwood) automatically; the hint under the port selector shows the radio's status. For a radio it doesn't recognise, select its port and choose the protocol yourself.
2. **Select a reflector** — Choose a saved reflector profile from the Profile dropdown, or fill in Type/ID/Domain/Module to create a new one.
3. **Enter your callsign** — Set your callsign and gateway module suffix.
4. **Click Connect** — The status panel will update once the link is established. The connected profile is remembered and reselected automatically on the next launch.
5. **Transmit** — Key up on your radio! Welcome to D-STAR!

The log panel shows timestamped activity including connections, heard callsigns, and errors.

### Saving reflector profiles

The Connect panel keeps a list of saved profiles you can switch between via the **Profile** dropdown:

- **Save profile** — captures the current Type, ID, Domain, and Module fields and writes the entry into `config.yaml` under `reflectors`. The profile name is derived from the form (e.g. `REF001 C`); saving with the same name overwrites the existing entry, otherwise a new one is appended.
- **Delete profile** — removes the currently selected entry from the dropdown and from `config.yaml`.
- **Last used** — the most recent profile you connected to is stored in `last_used_reflector` and pre-selected on next launch.

Because profiles live in `config.yaml`, you can migrate them between devices by copying that file. Callsign and gateway suffix are global and apply to every profile.

## License

See [LICENSE](LICENSE).

©️ 2026 Dave Streng (KR4GCQ)

This project is supported by me alone... Feel free to [Donate](https://www.paypal.com/donate/?hosted_button_id=J3YVE7V6F8NN2) if you're feeling generous! :)