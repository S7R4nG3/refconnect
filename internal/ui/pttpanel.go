package ui

import (
	"fmt"
	"strconv"

	"go.bug.st/serial"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

var commonBaudRates = []string{"1200", "2400", "4800", "9600", "19200", "38400", "57600", "115200", "230400", "460800"}

// buildPTTPanel returns the serial port and baud rate selectors.
// Port and baud selections are written back to a.cfg.Radio immediately on change
// so that the reflector Connect button can open the radio with current values.
func buildPTTPanel(a *App) fyne.CanvasObject {
	ports, _ := serial.GetPortsList()
	if len(ports) == 0 {
		ports = []string{"(no ports found)"}
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

	currentBaud := fmt.Sprintf("%d", a.cfg.Radio.BaudRate)
	baudSelect := widget.NewSelect(commonBaudRates, func(s string) {
		if b, err := strconv.Atoi(s); err == nil && b > 0 {
			a.cfg.Radio.BaudRate = b
		}
	})
	baudSelect.SetSelected(currentBaud)
	if baudSelect.Selected == "" {
		baudSelect.SetSelected("38400")
		a.cfg.Radio.BaudRate = 38400
	}

	return container.NewVBox(
		widget.NewLabel("Radio"),
		portSelect,
		container.NewGridWithColumns(2,
			widget.NewLabel("Baud Rate"),
			baudSelect,
		),
	)
}
