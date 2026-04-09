package ui

import (
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// buildStatusPanel returns the link status and activity display.
func buildStatusPanel(a *App) fyne.CanvasObject {
	linkRich := widget.NewRichText()
	updateLinkText := func(state string) {
		color := theme.ColorNameError // red — disconnected / error
		if strings.HasPrefix(state, "Connected") {
			color = theme.ColorNameSuccess // green
		}
		linkRich.Segments = []widget.RichTextSegment{
			&widget.TextSegment{
				Text:  "Link: " + state,
				Style: widget.RichTextStyle{ColorName: color, TextStyle: fyne.TextStyle{Bold: true}},
			},
		}
		linkRich.Refresh()
	}
	a.linkState.AddListener(binding.NewDataListener(func() {
		v, _ := a.linkState.Get()
		updateLinkText(v)
	}))
	updateLinkText("Disconnected") // set initial state

	heardLabel := widget.NewLabelWithData(binding.NewSprintf("Last heard: %s", a.lastHeard))

	rxIndicator := widget.NewProgressBarInfinite()
	rxIndicator.Hide()

	txIndicator := widget.NewProgressBarInfinite()
	txIndicator.Hide()

	// Show/hide activity indicators based on rx/tx bindings.
	a.rxActive.AddListener(binding.NewDataListener(func() {
		v, _ := a.rxActive.Get()
		if v {
			rxIndicator.Show()
			rxIndicator.Start()
		} else {
			rxIndicator.Stop()
			rxIndicator.Hide()
		}
	}))
	a.txActive.AddListener(binding.NewDataListener(func() {
		v, _ := a.txActive.Get()
		if v {
			txIndicator.Show()
			txIndicator.Start()
		} else {
			txIndicator.Stop()
			txIndicator.Hide()
		}
	}))

	return container.NewVBox(
		widget.NewLabel("Status"),
		linkRich,
		heardLabel,
		container.NewGridWithColumns(2,
			widget.NewLabel("RX:"), rxIndicator,
		),
		container.NewGridWithColumns(2,
			widget.NewLabel("TX:"), txIndicator,
		),
	)
}
