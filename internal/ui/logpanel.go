package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/widget"
)

// buildLogPanel returns a scrollable, auto-updating log view.
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

	clearBtn := widget.NewButton("Clear", func() {
		a.logLines.Set([]string{}) //nolint:errcheck
	})

	header := container.NewBorder(nil, nil, widget.NewLabel("Log"), clearBtn)

	return container.NewBorder(header, nil, nil, nil,
		container.NewVScroll(list),
	)
}
