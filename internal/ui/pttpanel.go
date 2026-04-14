package ui

import (
	"runtime"
	"strings"

	"go.bug.st/serial"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// protocolBaudRates maps config protocol values to their fixed baud rates.
var protocolBaudRates = map[string]int{
	"DV-GW": 38400,  // ICOM DV Gateway Terminal
	"MMDVM": 115200, // Kenwood MMDVM
}

// radioProtocols maps display labels to config values.
var radioProtocols = map[string]string{
	"ICOM (DV Gateway)":  "DV-GW",
	"Kenwood (MMDVM)":    "MMDVM",
}
var radioProtocolLabels = []string{"ICOM (DV Gateway)", "Kenwood (MMDVM)"}

// buildPTTPanel returns the serial port and baud rate selectors.
// Port and baud selections are written back to a.cfg.Radio immediately on change
// so that the reflector Connect button can open the radio with current values.
func buildPTTPanel(a *App) fyne.CanvasObject {
	ports, _ := serial.GetPortsList()
	if len(ports) == 0 {
		ports = []string{"(no ports found)"}
	}

	// Protocol selector — also sets the baud rate automatically.
	protoSelect := widget.NewSelect(radioProtocolLabels, func(label string) {
		if val, ok := radioProtocols[label]; ok {
			a.cfg.Radio.Protocol = val
			if baud, ok := protocolBaudRates[val]; ok {
				a.cfg.Radio.BaudRate = baud
			}
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
		a.cfg.Radio.BaudRate = 38400
	}

	labeledPorts := labelPorts(ports)

	portSelect := widget.NewSelect(labeledPorts, func(p string) {
		a.cfg.Radio.Port = stripPortLabel(p)
	})
	for _, lp := range labeledPorts {
		if stripPortLabel(lp) == a.cfg.Radio.Port {
			portSelect.SetSelected(lp)
			break
		}
	}
	if portSelect.Selected == "" && len(labeledPorts) > 0 {
		portSelect.SetSelected(labeledPorts[0])
		a.cfg.Radio.Port = stripPortLabel(labeledPorts[0])
	}

	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		newPorts, _ := serial.GetPortsList()
		if len(newPorts) == 0 {
			newPorts = []string{"(no ports found)"}
		}
		newLabeled := labelPorts(newPorts)
		portSelect.Options = newLabeled
		// Preserve current selection if still available.
		currentRaw := stripPortLabel(portSelect.Selected)
		found := false
		for _, lp := range newLabeled {
			if stripPortLabel(lp) == currentRaw {
				portSelect.SetSelected(lp)
				found = true
				break
			}
		}
		if !found && len(newLabeled) > 0 {
			portSelect.SetSelected(newLabeled[0])
			a.cfg.Radio.Port = stripPortLabel(newLabeled[0])
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

// isBTPort returns true if the port name looks like a Bluetooth SPP device.
func isBTPort(name string) bool {
	switch runtime.GOOS {
	case "darwin":
		return strings.Contains(name, "Bluetooth") || strings.Contains(name, "-SerialPort")
	case "linux":
		return strings.HasPrefix(name, "/dev/rfcomm")
	}
	return false
}

// labelPorts annotates Bluetooth ports with a (BT) suffix for display.
func labelPorts(ports []string) []string {
	out := make([]string, len(ports))
	for i, p := range ports {
		if isBTPort(p) {
			out[i] = p + "  (BT)"
		} else {
			out[i] = p
		}
	}
	return out
}

// stripPortLabel removes the (BT) annotation to get the raw port path.
func stripPortLabel(labeled string) string {
	return strings.TrimSuffix(labeled, "  (BT)")
}
