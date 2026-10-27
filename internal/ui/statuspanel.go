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
	linkRich.Wrapping = fyne.TextWrapWord // let longer error/suggestion text wrap
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

	return container.NewVBox(
		widget.NewLabel("Status"),
		linkRich,
		heardLabel,
		activityRow("RX:", a.rxActive),
		activityRow("TX:", a.txActive),
	)
}

// activityRow returns a labelled indicator that animates while active is true.
func activityRow(label string, active binding.Bool) *fyne.Container {
	indicator := widget.NewProgressBarInfinite()
	indicator.Hide()
	row := container.NewGridWithColumns(2, widget.NewLabel(label), indicator)
	active.AddListener(binding.NewDataListener(func() {
		if v, _ := active.Get(); v {
			indicator.Show()
			indicator.Start()
		} else {
			indicator.Stop()
			indicator.Hide()
		}
		// The grid skips hidden children when laying out, so an indicator
		// hidden at layout time stays 0x0 when shown until the row re-lays out.
		row.Refresh()
	}))
	return row
}
