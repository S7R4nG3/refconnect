package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
)

// buildLogPanel returns a collapsible log view. The log is hidden by default
// and revealed when the user clicks the "Logs" button.
func buildLogPanel(a *App) fyne.CanvasObject {
	list := widget.NewListWithData(
		a.logLines,
		func() fyne.CanvasObject {
			return widget.NewLabel("")
		},
		func(di binding.DataItem, o fyne.CanvasObject) {
			s, _ := di.(binding.String).Get()
			o.(*widget.Label).SetText(s)
		},
	)

	logContent := container.NewVScroll(list)
	logContent.SetMinSize(fyne.NewSize(0, 200))
	logContent.Hide()

	toggleBtn := widget.NewButton("Logs", func() {
		if logContent.Visible() {
			logContent.Hide()
		} else {
			logContent.Show()
		}
	})

	return container.NewBorder(toggleBtn, nil, nil, nil, logContent)
}
