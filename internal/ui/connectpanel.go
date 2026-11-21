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

// repeaterProtocols are the link protocols a repeater's gateway may accept,
// DPlus first: it is the only one both ircDDBGateway and QnetGateway accept
// from client apps (QnetGateway takes DExtra only from known gateways).
var repeaterProtocols = []struct {
	proto protocol.Protocol
	port  uint16
}{
	{protocol.ProtoDPlus, 20001},
	{protocol.ProtoDExtra, 30001},
	{protocol.ProtoDCS, 30051},
}

// repeaterEntry builds the entry for linking to module of the repeater call
// over proto. call is the repeater's callsign without module (at most 7
// characters, since the 8th is the module); ok is false if it isn't valid.
func repeaterEntry(call, module, host, proto string) (config.ReflectorEntry, bool) {
	call = strings.ToUpper(strings.TrimSpace(call))
	if call == "" || len(call) > 7 || strings.ContainsAny(call, " ") || module == "" || host == "" {
		return config.ReflectorEntry{}, false
	}
	for _, rp := range repeaterProtocols {
		if string(rp.proto) == proto {
			return config.ReflectorEntry{
				Name:         call + " " + module,
				Host:         host,
				Port:         rp.port,
				Module:       module,
				Protocol:     proto,
				RepeaterCall: call,
			}, true
		}
	}
	return config.ReflectorEntry{}, false
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
// match any known reflector family. Repeater entries (RepeaterCall set) are
// loaded directly, not through here.
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

	// hostLink mirrors the editable host field as a clickable link so the user
	// can verify it before connecting and jump to the reflector's status page.
	hostLink := widget.NewHyperlink("—", nil)

	selectedType := reflectorTypeByPrefix("REF")
	currentID := "001"

	// defaultHost builds the conventional hostname for a reflector family and
	// ID — e.g. REF + 001 + dstargateway.org -> "ref001.dstargateway.org". When
	// the family has no default domain (XLX), it returns just the "<prefix><id>"
	// stub for the user to complete.
	// repeaterMode switches the panel from linking to a reflector to linking
	// to a repeater's gateway: the ID field then holds the repeater callsign,
	// the Type row gives way to a Protocol row, and the host is user-entered.
	repeaterMode := false

	defaultHost := func(rt reflectorType, id string) string {
		// A repeater's gateway has no conventional hostname; the user enters it.
		if repeaterMode {
			return ""
		}
		base := strings.ToLower(rt.prefix) + strings.ToLower(strings.TrimSpace(id))
		if strings.TrimSpace(rt.defaultDomain) == "" {
			return base
		}
		return base + "." + rt.defaultDomain
	}

	// hostEntry holds the FULL host (hostname or raw IP) used for UDP. It is
	// auto-populated from the reflector type + ID, but is fully editable so the
	// user can override it — e.g. to bypass a Cloudflare-proxied hostname by
	// entering the reflector's origin IP directly.
	hostEntry := widget.NewEntry()
	hostEntry.SetPlaceHolder("hostname or IP")

	// hostManual tracks whether the user has typed into hostEntry directly. Once
	// they have, changing the ID no longer clobbers their value (changing the
	// Type still resets it, since that's an explicit family switch). The
	// programmatic guard keeps our own SetText calls from counting as edits.
	hostManual := false
	programmatic := false
	setHost := func(s string) {
		programmatic = true
		hostEntry.SetText(s)
		programmatic = false
	}

	updateHostLink := func() {
		host := strings.TrimSpace(hostEntry.Text)
		if host == "" {
			hostLink.SetText("—")
			hostLink.SetURL(nil)
			return
		}
		hostLink.SetText(host)
		if u, err := url.Parse("http://" + host); err == nil {
			hostLink.SetURL(u)
		} else {
			hostLink.SetURL(nil)
		}
	}

	hostEntry.OnChanged = func(string) {
		if !programmatic {
			hostManual = true
		}
		updateHostLink()
	}

	setHost(defaultHost(selectedType, currentID))

	idLabel := widget.NewLabel("ID:")
	idEntry := widget.NewEntry()
	idEntry.SetText("001")
	idEntry.OnChanged = func(s string) {
		currentID = strings.TrimSpace(s)
		if !hostManual {
			setHost(defaultHost(selectedType, currentID))
		}
	}

	// protoSelect picks a repeater's link protocol; reflector families have a
	// fixed one, so it is shown only in repeater mode.
	protoNames := make([]string, len(repeaterProtocols))
	for i, rp := range repeaterProtocols {
		protoNames[i] = string(rp.proto)
	}
	protoLabel := widget.NewLabel("Protocol:")
	protoSelect := widget.NewSelect(protoNames, nil)
	protoSelect.SetSelected(protoNames[0])

	typeLabel := widget.NewLabel("Type:")
	typeSelect := widget.NewSelect(typeNames, func(s string) {
		selectedType = reflectorTypeByPrefix(s)
		hostManual = false
		setHost(defaultHost(selectedType, currentID))
	})
	typeSelect.SetSelected("REF")

	// The heading doubles as the mode switch: "Reflector ⟷ Repeater", with
	// the active side in bold.
	reflectorHeading := widget.NewLabel("Reflector")
	repeaterHeading := widget.NewLabel("Repeater")

	// showMode lays the panel out for repeaterMode, then refreshes it so it
	// makes room for the rows it now shows or hides.
	var fields, panel *fyne.Container
	showMode := func() {
		reflectorHeading.TextStyle.Bold = !repeaterMode
		repeaterHeading.TextStyle.Bold = repeaterMode
		reflectorHeading.Refresh()
		repeaterHeading.Refresh()
		if repeaterMode {
			idLabel.SetText("Repeater:")
			idEntry.SetPlaceHolder("callsign, e.g. W4ABC")
			typeLabel.Hide()
			typeSelect.Hide()
			protoLabel.Show()
			protoSelect.Show()
		} else {
			idLabel.SetText("ID:")
			idEntry.SetPlaceHolder("001")
			typeLabel.Show()
			typeSelect.Show()
			protoLabel.Hide()
			protoSelect.Hide()
		}
		if panel != nil {
			fields.Refresh()
			panel.Refresh()
		}
	}
	showMode()

	// Each mode keeps its own ID and host, so flipping the switch back and
	// forth doesn't lose what was typed in either.
	type modeFields struct {
		id, host   string
		hostManual bool
	}
	saved := [2]modeFields{{id: "001"}} // [reflector, repeater]
	modeIndex := func(repeater bool) int {
		if repeater {
			return 1
		}
		return 0
	}
	var modeSwitch *toggleSwitch
	setMode := func(repeater bool) {
		if repeater == repeaterMode {
			return
		}
		saved[modeIndex(repeaterMode)] = modeFields{idEntry.Text, hostEntry.Text, hostManual}
		repeaterMode = repeater
		next := saved[modeIndex(repeater)]
		hostManual = true // keep the ID handler from rewriting the host
		idEntry.SetText(next.id)
		hostManual = next.hostManual
		if hostManual || repeater {
			setHost(next.host)
		} else {
			setHost(defaultHost(selectedType, currentID))
		}
		modeSwitch.SetChecked(repeater)
		showMode()
	}
	modeSwitch = newToggleSwitch(false, setMode)

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
		host := strings.TrimSpace(hostEntry.Text)
		if repeaterMode {
			return repeaterEntry(id, module, host, protoSelect.Selected)
		}
		if id == "" || module == "" || host == "" {
			return config.ReflectorEntry{}, false
		}
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
			if r.RepeaterCall != "" {
				setMode(true)
				hostManual = true
				idEntry.SetText(strings.ToUpper(r.RepeaterCall))
				moduleSelect.SetSelected(r.Module)
				protoSelect.SetSelected(r.Protocol)
				setHost(r.Host)
				return
			}
			p, id, _, mod := parseEntry(r)
			if p == "" {
				return
			}
			setMode(false)
			typeSelect.SetSelected(p) // resets host + hostManual
			selectedType = reflectorTypeByPrefix(p)
			idEntry.SetText(id)
			moduleSelect.SetSelected(mod)
			// Apply the saved host last so the type/ID handlers above don't
			// overwrite it. If it deviates from the conventional hostname (e.g.
			// a raw origin IP), flag it manual so a later ID keystroke keeps it.
			hostManual = strings.TrimSpace(r.Host) != defaultHost(selectedType, id)
			setHost(r.Host)
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

	updateHostLink()

	saveBtn := widget.NewButton("Save profile", func() {
		e, ok := captureEntry()
		if !ok {
			if repeaterMode {
				a.appendLog("Enter the repeater callsign (up to 7 characters, without module), module and host before saving.")
				return
			}
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
			if repeaterMode {
				a.appendLog("Please enter the repeater callsign.")
			} else {
				a.appendLog("Please enter a reflector ID.")
			}
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
		if !ok && repeaterMode {
			a.appendLog("Enter the repeater callsign (up to 7 characters, without module) and the gateway's host before connecting.")
			return
		}
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

	fields = container.NewGridWithColumns(2,
		widget.NewLabel("Profile:"), profileSelect,
		typeLabel, typeSelect,
		protoLabel, protoSelect,
		idLabel, idEntry,
		widget.NewLabel("Host:"), hostEntry,
		widget.NewLabel("Link:"), hostLink,
		widget.NewLabel("Module:"), moduleSelect,
		widget.NewLabel("Callsign:"), container.NewBorder(nil, nil, nil, suffixSelect, callsignEntry),
	)
	panel = container.NewVBox(
		container.NewHBox(reflectorHeading, modeSwitch, repeaterHeading),
		fields,
		container.NewGridWithColumns(2, saveBtn, deleteBtn),
		toggleBtn,
	)
	return panel
}
