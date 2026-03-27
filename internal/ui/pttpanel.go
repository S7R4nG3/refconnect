package ui

import (
	"go.bug.st/serial"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"
)

// buildPTTPanel returns the serial port selector and the PTT button.
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

	openBtn := widget.NewButton("Open", nil)
	closeBtn := widget.NewButton("Close", nil)
	closeBtn.Disable()

	openBtn.OnTapped = func() {
		port := portSelect.Selected
		if port == "" || port == "(no ports found)" {
			a.appendLog("No serial port selected.")
			return
		}
		a.cfg.Radio.Port = port
		a.openRadio(port)
		openBtn.Disable()
		closeBtn.Enable()
	}
	closeBtn.OnTapped = func() {
		a.closeRadio()
		openBtn.Enable()
		closeBtn.Disable()
	}

	pttBtn := widget.NewButton("PTT", nil)
	pttBtn.Importance = widget.DangerImportance
	pttActive := false
	pttBtn.OnTapped = func() {
		pttActive = !pttActive
		a.ptt(pttActive)
		if pttActive {
			pttBtn.SetText("PTT  [ON]")
		} else {
			pttBtn.SetText("PTT")
		}
	}

	// Spacebar shortcut for PTT.
	a.win.Canvas().SetOnTypedKey(func(ev *fyne.KeyEvent) {
		if ev.Name == fyne.KeySpace {
			pttBtn.OnTapped()
		}
	})

	return container.NewVBox(
		widget.NewLabel("Radio"),
		container.NewGridWithColumns(3, portSelect, openBtn, closeBtn),
		pttBtn,
	)
}
