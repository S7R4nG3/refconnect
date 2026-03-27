package ui

import (
	"fmt"
	"strings"

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
	{"XLX", protocol.ProtoXLX, 30001, "xlxreflector.org"},
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

	suffixOptions := make([]string, 26)
	for i := range suffixOptions {
		suffixOptions[i] = string(rune('A' + i))
	}
	suffixSelect := widget.NewSelect(suffixOptions, nil)
	suffixSelect.SetSelected(a.cfg.CallsignSuffix)
	if suffixSelect.Selected == "" {
		suffixSelect.SetSelected("A")
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

	connectBtn := widget.NewButton("Connect", nil)
	disconnectBtn := widget.NewButton("Disconnect", nil)
	disconnectBtn.Disable()

	connectBtn.OnTapped = func() {
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

		connectBtn.Disable()
		disconnectBtn.Enable()
		a.connect(entry)
	}

	disconnectBtn.OnTapped = func() {
		a.disconnect()
		connectBtn.Enable()
		disconnectBtn.Disable()
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
		container.NewGridWithColumns(2, connectBtn, disconnectBtn),
	)
}
