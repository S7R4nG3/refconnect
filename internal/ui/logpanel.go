package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
)

// buildLogPanel returns the scrollable log content (hidden by default) and a
// small toggle button intended for placement in the window footer. Clicking
// the button shows or hides the log content.
func buildLogPanel(a *App) (content fyne.CanvasObject, toggleBtn *widget.Button) {
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

	// Auto-scroll to the newest entry when the list changes.
	a.logLines.AddListener(binding.NewDataListener(func() {
		lines, _ := a.logLines.Get()
		if n := len(lines); n > 0 {
			list.ScrollToBottom()
		}
	}))

	logContent := container.NewVScroll(list)
	logContent.SetMinSize(fyne.NewSize(0, 200))
	logContent.Hide()

	const logHeight float32 = 200

	btn := widget.NewButton("Logs", func() {
		sz := a.win.Canvas().Size()
		if logContent.Visible() {
			logContent.Hide()
			a.win.Resize(fyne.NewSize(sz.Width, sz.Height-logHeight))
		} else {
			logContent.Show()
			a.win.Resize(fyne.NewSize(sz.Width, sz.Height+logHeight))
		}
	})

	return logContent, btn
}
