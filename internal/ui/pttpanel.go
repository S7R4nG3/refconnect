package ui

import (
	"go.bug.st/serial"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/S7R4nG3/refconnect/internal/config"
)

// radioProtocols maps display labels to config values.
var radioProtocols = map[string]string{
	"ICOM (DV Gateway)": "DV-GW",
	"Kenwood (MMDVM)":   "MMDVM",
}
var radioProtocolLabels = []string{"ICOM (DV Gateway)", "Kenwood (MMDVM)"}

// refreshPorts enumerates serial ports and updates the Select widget.
// It preserves the current selection when possible and falls back to
// the first available port (or the config-saved port) otherwise.
func refreshPorts(portSelect *widget.Select, cfg *config.Config) {
	ports, _ := serial.GetPortsList()
	if len(ports) == 0 {
		ports = []string{"(no ports found)"}
	}
	portSelect.Options = ports

	// Try to keep the current selection; fall back to config, then first port.
	current := portSelect.Selected
	best := ""
	for _, p := range ports {
		if p == current {
			best = p
			break
		}
		if p == cfg.Radio.Port && best == "" {
			best = p
		}
	}
	if best == "" && len(ports) > 0 && ports[0] != "(no ports found)" {
		best = ports[0]
	}
	if best != "" {
		portSelect.SetSelected(best)
		cfg.Radio.Port = best
	}
	portSelect.Refresh()
}

// buildPTTPanel returns the serial port and protocol selectors.
// Selections are written back to a.cfg.Radio immediately on change
// so that the reflector Connect button can open the radio with current values.
func buildPTTPanel(a *App) fyne.CanvasObject {
	protoSelect := widget.NewSelect(radioProtocolLabels, func(label string) {
		if val, ok := radioProtocols[label]; ok {
			a.cfg.Radio.Protocol = val
		}
	})
	// Set current selection from config.
	for label, val := range radioProtocols {
		if val == a.cfg.Radio.Protocol {
			protoSelect.SetSelected(label)
			break
		}
	}
	if protoSelect.Selected == "" {
		protoSelect.SetSelected(radioProtocolLabels[0])
		a.cfg.Radio.Protocol = "DV-GW"
	}

	// Start with a placeholder; enumerate ports in the background so
	// the window appears immediately (serial enumeration is slow on Windows).
	portSelect := widget.NewSelect([]string{"scanning…"}, func(p string) {
		if p != "scanning…" && p != "(no ports found)" {
			a.cfg.Radio.Port = p
		}
	})

	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		refreshPorts(portSelect, a.cfg)
	})

	// Kick off the initial port scan in the background.
	go refreshPorts(portSelect, a.cfg)

	portRow := container.NewBorder(nil, nil, nil, refreshBtn, portSelect)

	aprsCheck := widget.NewCheckWithData("Enable APRS", a.aprsEnabled)

	return container.NewVBox(
		widget.NewLabel("Radio"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Protocol"),
			protoSelect,
		),
		portRow,
		aprsCheck,
	)
}

