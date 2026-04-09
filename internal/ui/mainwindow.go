package ui

import (
	"net/url"

	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"fyne.io/fyne/v2"
)

func parseURL(raw string) *url.URL {
	u, _ := url.Parse(raw)
	return u
}

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

	repoURL := parseURL("https://github.com/S7R4nG3/refconnect")
	ghBtn := widget.NewButtonWithIcon("", resourceGithubMarkSvg, func() {
		a.fyneApp.OpenURL(repoURL)
	})

	footer := container.NewHBox(
		widget.NewLabel("© Dave Streng (KR4GCQ)"),
		ghBtn,
	)

	return container.NewBorder(nil, container.NewVBox(container.NewPadded(log), container.NewCenter(footer)), nil, nil, top)
}
