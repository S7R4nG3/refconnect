package ui

import (
	"go.bug.st/serial"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// radioProtocols maps display labels to config values.
var radioProtocols = map[string]string{
	"ICOM (DV Gateway)": "DV-GW",
	"Kenwood (MMDVM)":   "MMDVM",
}
var radioProtocolLabels = []string{"ICOM (DV Gateway)", "Kenwood (MMDVM)"}

// buildPTTPanel returns the serial port and protocol selectors.
// Selections are written back to a.cfg.Radio immediately on change
// so that the reflector Connect button can open the radio with current values.
func buildPTTPanel(a *App) fyne.CanvasObject {
	ports, _ := serial.GetPortsList()
	if len(ports) == 0 {
		ports = []string{"(no ports found)"}
	}

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

	portSelect := widget.NewSelect(ports, func(p string) {
		a.cfg.Radio.Port = p
	})
	for _, p := range ports {
		if p == a.cfg.Radio.Port {
			portSelect.SetSelected(p)
			break
		}
	}
	if portSelect.Selected == "" && len(ports) > 0 {
		portSelect.SetSelected(ports[0])
		a.cfg.Radio.Port = ports[0]
	}

	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		newPorts, _ := serial.GetPortsList()
		if len(newPorts) == 0 {
			newPorts = []string{"(no ports found)"}
		}
		portSelect.Options = newPorts
		// Preserve current selection if still available.
		current := portSelect.Selected
		found := false
		for _, p := range newPorts {
			if p == current {
				portSelect.SetSelected(p)
				found = true
				break
			}
		}
		if !found && len(newPorts) > 0 {
			portSelect.SetSelected(newPorts[0])
			a.cfg.Radio.Port = newPorts[0]
		}
		portSelect.Refresh()
	})

	portRow := container.NewBorder(nil, nil, nil, refreshBtn, portSelect)

	return container.NewVBox(
		widget.NewLabel("Radio"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Protocol"),
			protoSelect,
		),
		portRow,
	)
}

