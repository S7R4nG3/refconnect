package ui

import (
	"fmt"
	"strconv"

	"go.bug.st/serial"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// buildPTTPanel returns the serial port selector, baud rate field, and open/close buttons.
func buildPTTPanel(a *App) fyne.CanvasObject {
	ports, _ := serial.GetPortsList()
	if len(ports) == 0 {
		ports = []string{"(no ports found)"}
	}

	// Pre-select the configured port if it appears in the list.
	portSelect := widget.NewSelect(ports, nil)
	for _, p := range ports {
		if p == a.cfg.Radio.Port {
			portSelect.SetSelected(p)
			break
		}
	}
	if portSelect.Selected == "" && len(ports) > 0 {
		portSelect.SetSelected(ports[0])
	}

	baudEntry := widget.NewEntry()
	baudEntry.SetText(fmt.Sprintf("%d", a.cfg.Radio.BaudRate))
	baudEntry.SetPlaceHolder("9600")

	openBtn := widget.NewButton("Open", nil)
	closeBtn := widget.NewButton("Close", nil)
	closeBtn.Disable()

	openBtn.OnTapped = func() {
		port := portSelect.Selected
		if port == "" || port == "(no ports found)" {
			a.appendLog("No serial port selected.")
			return
		}
		baud, err := strconv.Atoi(baudEntry.Text)
		if err != nil || baud <= 0 {
			a.appendLog("Invalid baud rate.")
			return
		}
		a.cfg.Radio.Port = port
		a.cfg.Radio.BaudRate = baud
		openBtn.Disable()
		go func() {
			a.openRadio(port)
			if a.radio == nil || !a.radio.IsOpen() {
				openBtn.Enable()
			} else {
				closeBtn.Enable()
			}
		}()
	}
	closeBtn.OnTapped = func() {
		a.closeRadio()
		openBtn.Enable()
		closeBtn.Disable()
	}

	return container.NewVBox(
		widget.NewLabel("Radio"),
		portSelect,
		container.NewGridWithColumns(2,
			widget.NewLabel("Baud Rate"),
			baudEntry,
		),
		container.NewGridWithColumns(2, openBtn, closeBtn),
	)
}
