package ui

import (
	"fmt"
	"net/url"
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
	{"DCS", protocol.ProtoDCS, 30051, "xreflector.net"},
}

func reflectorTypeByPrefix(prefix string) reflectorType {
	for _, rt := range reflectorTypes {
		if rt.prefix == prefix {
			return rt
		}
	}
	return reflectorTypes[0] // default to XRF/DExtra
}

// parseEntry splits a saved reflector entry back into the UI field values
// (type prefix, ID, domain, module). Returns empty prefix if Name does not
// match any known reflector family.
func parseEntry(e config.ReflectorEntry) (prefix, id, domain, module string) {
	name := strings.ToUpper(strings.TrimSpace(e.Name))
	for _, rt := range reflectorTypes {
		if strings.HasPrefix(name, rt.prefix) {
			prefix = rt.prefix
			parts := strings.Fields(strings.TrimPrefix(name, rt.prefix))
			if len(parts) > 0 {
				id = parts[0]
			}
			break
		}
	}
	if prefix == "" {
		return "", "", "", ""
	}
	host := strings.ToLower(e.Host)
	head := strings.ToLower(prefix) + strings.ToLower(id) + "."
	if strings.HasPrefix(host, head) {
		domain = host[len(head):]
	} else {
		domain = host
	}
	module = e.Module
	return prefix, id, domain, module
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

	// hostLink shows the dynamically constructed hostname as a clickable link
	// so the user can verify it before connecting and jump to the reflector's
	// status page.
	hostLink := widget.NewHyperlink("—", nil)

	selectedType := reflectorTypeByPrefix("REF")
	currentID := "001"

	domainEntry := widget.NewEntry()
	domainEntry.SetText(selectedType.defaultDomain)

	updateHost := func() {
		if currentID == "" {
			hostLink.SetText("—")
			hostLink.SetURL(nil)
			return
		}
		host := fmt.Sprintf("%s%s.%s",
			strings.ToLower(selectedType.prefix),
			strings.ToLower(currentID),
			strings.TrimSpace(domainEntry.Text),
		)
		hostLink.SetText(host)
		if u, err := url.Parse("http://" + host); err == nil {
			hostLink.SetURL(u)
		} else {
			hostLink.SetURL(nil)
		}
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
	if suffixSelect.Selected == "" || suffixSelect.Selected == " " {
		suffixSelect.SetSelected("D")
	}

	callsignEntry := widget.NewEntry()
	callsignEntry.SetText(a.cfg.Callsign)
	callsignEntry.SetPlaceHolder("N0CALL")

	// captureEntry produces a ReflectorEntry from the current UI fields.
	captureEntry := func() (config.ReflectorEntry, bool) {
		id := strings.TrimSpace(idEntry.Text)
		module := moduleSelect.Selected
		if id == "" || module == "" {
			return config.ReflectorEntry{}, false
		}
		host := fmt.Sprintf("%s%s.%s",
			strings.ToLower(selectedType.prefix),
			strings.ToLower(id),
			strings.TrimSpace(domainEntry.Text),
		)
		name := fmt.Sprintf("%s%s %s", selectedType.prefix, strings.ToUpper(id), module)
		return config.ReflectorEntry{
			Name:     name,
			Host:     host,
			Port:     selectedType.port,
			Module:   module,
			Protocol: string(selectedType.proto),
		}, true
	}

	profileNames := func() []string {
		names := make([]string, len(a.cfg.Reflectors))
		for i, r := range a.cfg.Reflectors {
			names[i] = r.Name
		}
		return names
	}

	profileSelect := widget.NewSelect(profileNames(), nil)
	profileSelect.PlaceHolder = "(none)"
	loadProfile := func(name string) {
		for _, r := range a.cfg.Reflectors {
			if r.Name != name {
				continue
			}
			p, id, dom, mod := parseEntry(r)
			if p == "" {
				return
			}
			typeSelect.SetSelected(p)
			selectedType = reflectorTypeByPrefix(p)
			idEntry.SetText(id)
			domainEntry.SetText(dom)
			moduleSelect.SetSelected(mod)
			updateHost()
			return
		}
	}
	profileSelect.OnChanged = loadProfile

	// Select initial profile: prefer LastUsedReflector, fall back to first entry.
	initialProfile := ""
	for _, r := range a.cfg.Reflectors {
		if r.Name == a.cfg.LastUsedReflector {
			initialProfile = r.Name
			break
		}
	}
	if initialProfile == "" && len(a.cfg.Reflectors) > 0 {
		initialProfile = a.cfg.Reflectors[0].Name
	}
	if initialProfile != "" {
		profileSelect.SetSelected(initialProfile)
	}

	updateHost()

	saveBtn := widget.NewButton("Save profile", func() {
		e, ok := captureEntry()
		if !ok {
			a.appendLog("Enter an ID and module before saving.")
			return
		}
		updated := false
		for i, r := range a.cfg.Reflectors {
			if r.Name == e.Name {
				a.cfg.Reflectors[i] = e
				updated = true
				break
			}
		}
		if !updated {
			a.cfg.Reflectors = append(a.cfg.Reflectors, e)
		}
		if err := config.Save(a.cfg); err != nil {
			a.appendLog("Save failed: " + err.Error())
			return
		}
		profileSelect.Options = profileNames()
		profileSelect.Refresh()
		profileSelect.SetSelected(e.Name)
		if updated {
			a.appendLog("Profile updated: " + e.Name)
		} else {
			a.appendLog("Profile saved: " + e.Name)
		}
	})

	deleteBtn := widget.NewButton("Delete profile", func() {
		name := profileSelect.Selected
		if name == "" {
			return
		}
		for i, r := range a.cfg.Reflectors {
			if r.Name == name {
				a.cfg.Reflectors = append(a.cfg.Reflectors[:i], a.cfg.Reflectors[i+1:]...)
				break
			}
		}
		if a.cfg.LastUsedReflector == name {
			a.cfg.LastUsedReflector = ""
		}
		if err := config.Save(a.cfg); err != nil {
			a.appendLog("Save failed: " + err.Error())
			return
		}
		profileSelect.Options = profileNames()
		profileSelect.ClearSelected()
		profileSelect.Refresh()
		a.appendLog("Profile deleted: " + name)
	})

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

		entry, ok := captureEntry()
		if !ok {
			a.appendLog("Please complete reflector fields before connecting.")
			return
		}
		a.cfg.LastUsedReflector = entry.Name
		if err := config.Save(a.cfg); err != nil {
			a.appendLog("Config save failed: " + err.Error())
		}

		toggleBtn.Disable()
		go func() {
			a.openRadio(a.cfg.Radio.Port)
			if a.radio == nil || !a.radio.IsOpen() {
				fyne.Do(func() { toggleBtn.Enable() })
				return
			}
			a.connect(entry)
			connected.Store(true)
			fyne.Do(func() {
				toggleBtn.SetText("Disconnect")
				toggleBtn.Enable()
			})
		}()
	}

	return container.NewVBox(
		widget.NewLabel("Reflector"),
		container.NewGridWithColumns(2,
			widget.NewLabel("Profile:"), profileSelect,
			widget.NewLabel("Type:"), typeSelect,
			widget.NewLabel("ID:"), idEntry,
			widget.NewLabel("Domain:"), domainEntry,
			widget.NewLabel("Host:"), hostLink,
			widget.NewLabel("Module:"), moduleSelect,
			widget.NewLabel("Callsign:"), container.NewBorder(nil, nil, nil, suffixSelect, callsignEntry),
		),
		container.NewGridWithColumns(2, saveBtn, deleteBtn),
		toggleBtn,
	)
}
