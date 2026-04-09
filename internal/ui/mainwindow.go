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
	logContent, logToggleBtn := buildLogPanel(a)

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

	qrzURL := parseURL("https://www.qrz.com/db/KR4GCQ")

	centeredNotice := container.NewCenter(container.NewHBox(
		widget.NewLabel("© 2026 Dave Streng"),
		widget.NewHyperlink("KR4GCQ", qrzURL),
		ghBtn,
	))
	footer := container.NewBorder(nil, nil, nil, logToggleBtn, centeredNotice)

	return container.NewBorder(nil, container.NewVBox(logContent, footer), nil, nil, top)
}
