package ui

import (
	"fmt"
	"strings"
	"sync/atomic"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/widget"

	"github.com/S7R4nG3/refconnect/internal/config"
	"github.com/S7R4nG3/refconnect/internal/protocol"
)

// reflectorType holds the static properties of a D-STAR reflector family.
type reflectorType struct {
	prefix        string
	proto         protocol.Protocol
	port          uint16
	defaultDomain string // most common DNS domain for this reflector family
}

var reflectorTypes = []reflectorType{
	{"XRF", protocol.ProtoDExtra, 30001, "openquad.net"},
	{"REF", protocol.ProtoDPlus, 20001, "dstargateway.org"},
	{"XLX", protocol.ProtoDExtra, 30001, ""},
}

func reflectorTypeByPrefix(prefix string) reflectorType {
	for _, rt := range reflectorTypes {
		if rt.prefix == prefix {
			return rt
		}
	}
	return reflectorTypes[0] // default to XRF/DExtra
}

// buildConnectPanel returns the reflector connection controls.
func buildConnectPanel(a *App) fyne.CanvasObject {
	modules := make([]string, 26)
	for i := range modules {
		modules[i] = string(rune('A' + i))
	}

	typeNames := make([]string, len(reflectorTypes))
	for i, rt := range reflectorTypes {
		typeNames[i] = rt.prefix
	}

	// hostLabel shows the dynamically constructed hostname so the user can
	// verify it before connecting.
	hostLabel := widget.NewLabel("")

	selectedType := reflectorTypeByPrefix("REF")
	currentID := "001"

	domainEntry := widget.NewEntry()
	domainEntry.SetText(selectedType.defaultDomain)

	updateHost := func() {
		if currentID == "" {
			hostLabel.SetText("—")
			return
		}
		host := fmt.Sprintf("%s%s.%s",
			strings.ToLower(selectedType.prefix),
			strings.ToLower(currentID),
			strings.TrimSpace(domainEntry.Text),
		)
		hostLabel.SetText(host)
	}

	domainEntry.OnChanged = func(string) { updateHost() }

	typeSelect := widget.NewSelect(typeNames, func(s string) {
		selectedType = reflectorTypeByPrefix(s)
		domainEntry.SetText(selectedType.defaultDomain)
		updateHost()
	})
	typeSelect.SetSelected("REF")

	idEntry := widget.NewEntry()
	idEntry.SetText("001")
	idEntry.OnChanged = func(s string) {
		currentID = strings.TrimSpace(s)
		updateHost()
	}

	moduleSelect := widget.NewSelect(modules, nil)
	moduleSelect.SetSelected("C")

	suffixOptions := make([]string, 27)
	suffixOptions[0] = " " // empty/space — matches bare callsign registration
	for i := 0; i < 26; i++ {
		suffixOptions[i+1] = string(rune('A' + i))
	}
	suffixSelect := widget.NewSelect(suffixOptions, nil)
	suffixSelect.SetSelected(a.cfg.CallsignSuffix)
	if suffixSelect.Selected == "" {
		suffixSelect.SetSelected(" ")
	}

	callsignEntry := widget.NewEntry()
	callsignEntry.SetText(a.cfg.Callsign)
	callsignEntry.SetPlaceHolder("N0CALL")

	// Pre-fill from the first saved profile if available.
	if len(a.cfg.Reflectors) > 0 {
		r := a.cfg.Reflectors[0]
		for _, rt := range reflectorTypes {
			if strings.HasPrefix(strings.ToUpper(r.Name), rt.prefix) {
				typeSelect.SetSelected(rt.prefix)
				selectedType = rt
				break
			}
		}
		name := strings.ToUpper(strings.TrimSpace(r.Name))
		for _, rt := range reflectorTypes {
			if strings.HasPrefix(name, rt.prefix) {
				parts := strings.Fields(strings.TrimPrefix(name, rt.prefix))
				if len(parts) > 0 {
					currentID = parts[0]
					idEntry.SetText(currentID)
				}
				break
			}
		}
		moduleSelect.SetSelected(r.Module)
	}

	updateHost()

	var connected atomic.Bool
	toggleBtn := widget.NewButton("Connect", nil)

	toggleBtn.OnTapped = func() {
		if connected.Load() {
			toggleBtn.Disable()
			a.disconnect()
			a.closeRadio()
			connected.Store(false)
			toggleBtn.SetText("Connect")
			toggleBtn.Enable()
			return
		}

		id := strings.TrimSpace(idEntry.Text)
		module := moduleSelect.Selected

		if id == "" {
			a.appendLog("Please enter a reflector ID.")
			return
		}
		if module == "" {
			a.appendLog("Please select a module.")
			return
		}
		if a.cfg.Radio.Port == "" || a.cfg.Radio.Port == "(no ports found)" {
			a.appendLog("No serial port selected.")
			return
		}

		a.cfg.Callsign = strings.TrimSpace(callsignEntry.Text)
		a.cfg.CallsignSuffix = suffixSelect.Selected

		host := fmt.Sprintf("%s%s.%s",
			strings.ToLower(selectedType.prefix),
			strings.ToLower(id),
			strings.TrimSpace(domainEntry.Text),
		)
		name := fmt.Sprintf("%s%s %s", selectedType.prefix, strings.ToUpper(id), module)

		entry := config.ReflectorEntry{
			Name:     name,
			Host:     host,
			Port:     selectedType.port,
			Module:   module,
			Protocol: string(selectedType.proto),
		}

		toggleBtn.Disable()
		go func() {
			a.openRadio(a.cfg.Radio.Port)
			if a.radio == nil || !a.radio.IsOpen() {
				toggleBtn.Enable()
				return
			}
			a.connect(entry)
			connected.Store(true)
			toggleBtn.SetText("Disconnect")
			toggleBtn.Enable()
		}()
	}

	return container.NewVBox(
		widget.NewLabel("Reflector"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Type:"), typeSelect,
			widget.NewLabel("ID:"), idEntry,
			widget.NewLabel("Domain:"), domainEntry,
			widget.NewLabel("Host:"), hostLabel,
			widget.NewLabel("Module:"), moduleSelect,
			widget.NewLabel("Callsign:"), container.NewBorder(nil, nil, nil, suffixSelect, callsignEntry),
		),
		toggleBtn,
	)
}
