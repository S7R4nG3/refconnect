package ui

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
)

// buildMainWindow assembles the full window content from the four panels.
//
// Layout:
//   ┌──────────────────┬──────────────────┐
//   │  Connect panel   │  Status panel    │  ← top row (HSplit)
//   │                  ├──────────────────┤
//   │                  │  Radio/PTT panel │
//   ├──────────────────┴──────────────────┤
//   │              Log panel              │  ← bottom (full width, expands)
//   └─────────────────────────────────────┘
func buildMainWindow(a *App) fyne.CanvasObject {
	connect := buildConnectPanel(a)
	status := buildStatusPanel(a)
	ptt := buildPTTPanel(a)
	log := buildLogPanel(a)

	top := container.NewHSplit(
		container.NewPadded(connect),
		container.NewVBox(
			container.NewPadded(status),
			container.NewPadded(ptt),
		),
	)
	top.SetOffset(0.6)

	outer := container.NewVSplit(
		top,
		container.NewPadded(log),
	)
	outer.SetOffset(0.75)
	return outer
}
