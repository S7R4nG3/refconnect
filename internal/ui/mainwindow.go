package ui

import (
	"fyne.io/fyne/v2/container"

	"fyne.io/fyne/v2"
)

// buildMainWindow assembles the full window content from the four panels.
//
// Layout:
//
//	┌──────────────────┬──────────────────┐
//	│  Connect panel   │  Status panel    │  ← top row (HSplit)
//	│                  ├──────────────────┤
//	│                  │  Radio/PTT panel │
//	├──────────────────┴──────────────────┤
//	│         [Logs] (collapsed)          │  ← toggle button + collapsible log
//	└─────────────────────────────────────┘
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

	return container.NewBorder(nil, container.NewPadded(log), nil, nil, top)
}
