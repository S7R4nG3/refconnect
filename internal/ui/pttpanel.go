package ui

import (
	"runtime"
	"strings"

	"go.bug.st/serial"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/S7R4nG3/refconnect/internal/radio"
)

// radioProtocols maps display labels to config values.
var radioProtocols = map[string]string{
	"ICOM (DV Gateway)":  "DV-GW",
	"Kenwood (MMDVM)":    "MMDVM",
}
var radioProtocolLabels = []string{"ICOM (DV Gateway)", "Kenwood (MMDVM)"}

// buildPTTPanel returns the serial port and protocol selectors.
// Selections are written back to a.cfg.Radio immediately on change
// so that the reflector Connect button can open the radio with current values.
func buildPTTPanel(a *App) fyne.CanvasObject {
	ports, btNames := listAllPorts()

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

	labeledPorts := labelPorts(ports, btNames)

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
		newPorts, newBTNames := listAllPorts()
		newLabeled := labelPorts(newPorts, newBTNames)
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

// listAllPorts returns all available serial ports plus paired Bluetooth devices,
// and a map of BT addresses to device names for labeling.
func listAllPorts() (ports []string, btNames map[string]string) {
	ports, _ = serial.GetPortsList()
	btNames = make(map[string]string)
	for _, bt := range radio.ListBTDevices() {
		ports = append(ports, bt.Addr)
		btNames[bt.Addr] = bt.Name
	}
	if len(ports) == 0 {
		ports = []string{"(no ports found)"}
	}
	return
}

// isBTPort returns true if the port name looks like a Bluetooth SPP device.
func isBTPort(name string) bool {
	switch runtime.GOOS {
	case "darwin":
		return strings.Contains(name, "Bluetooth") || strings.Contains(name, "-SerialPort")
	case "linux":
		return strings.HasPrefix(name, "/dev/rfcomm") || radio.IsBTAddress(name)
	}
	return false
}

// labelPorts annotates Bluetooth ports with a suffix for display.
// btNames maps BT MAC addresses to their human-readable device names.
func labelPorts(ports []string, btNames map[string]string) []string {
	out := make([]string, len(ports))
	for i, p := range ports {
		if name, ok := btNames[p]; ok {
			out[i] = p + "  (BT: " + name + ")"
		} else if isBTPort(p) {
			out[i] = p + "  (BT)"
		} else {
			out[i] = p
		}
	}
	return out
}

// stripPortLabel removes the BT annotation to get the raw port path or address.
func stripPortLabel(labeled string) string {
	if idx := strings.Index(labeled, "  (BT"); idx >= 0 {
		return labeled[:idx]
	}
	return labeled
}
