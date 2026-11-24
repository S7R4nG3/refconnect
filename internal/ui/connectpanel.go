package ui

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
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

// repeaterProtocols lists DPlus first: both ircDDBGateway and QnetGateway
// accept it from clients.
var repeaterProtocols = []struct {
	proto protocol.Protocol
	port  uint16
}{
	{protocol.ProtoDPlus, 20001},
	{protocol.ProtoDExtra, 30001},
	{protocol.ProtoDCS, 30051},
}

// repeaterEntry builds a repeater entry. call excludes the module (max 7
// chars); ok is false if any field is invalid.
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

// sortAlphaIcon is Material Design's "sort_by_alpha" icon (Apache 2.0);
// Fyne's theme has no alphabetical-sort icon.
var sortAlphaIcon = theme.NewThemedResource(fyne.NewStaticResource("sort_by_alpha.svg", []byte(
	`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24"><path fill="#000000" d="M14.94 4.66h-4.72l2.36-2.36zm-4.69 14.71h4.66l-2.33 2.33zM6.1 6.27L1.6 17.73h1.84l.92-2.45h5.11l.92 2.45h1.84L7.74 6.27H6.1zm-1.13 7.37l1.94-5.18 1.94 5.18H4.97zm10.76 2.5h6.12v1.59h-8.53v-1.29l5.92-8.56h-5.88v-1.6h8.3v1.26l-5.93 8.6z"/></svg>`)))

// profileSorts are the sort buttons beside the profile list, with their
// UIConfig.ProfileSort values.
var profileSorts = []struct {
	tip, value string
	icon       fyne.Resource
}{
	{"Sort profiles in saved order", "", theme.ListIcon()},
	{"Sort profiles A–Z by name", "name", sortAlphaIcon},
	{"Sort profiles by most recently connected", "recent", theme.HistoryIcon()},
}

// profileLabel is a profile's text in the list: the friendly name with the
// link in brackets ("Club net (REF001 C)"), or just the link.
func profileLabel(e config.ReflectorEntry) string {
	if l := strings.TrimSpace(e.Label); l != "" && l != e.Name {
		return l + " (" + e.Name + ")"
	}
	return e.Name
}

// profilesFor returns the saved profiles for one mode (repeater or reflector)
// in the given order: "" keeps saved order, "name" sorts by label, "recent"
// puts the last connected first.
func profilesFor(all []config.ReflectorEntry, repeater bool, order string) []config.ReflectorEntry {
	var out []config.ReflectorEntry
	for _, e := range all {
		if (e.RepeaterCall != "") == repeater {
			out = append(out, e)
		}
	}
	switch order {
	case "name":
		sort.SliceStable(out, func(i, j int) bool {
			return strings.ToLower(profileLabel(out[i])) < strings.ToLower(profileLabel(out[j]))
		})
	case "recent":
		sort.SliceStable(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	}
	return out
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
	// repeaterMode links to a repeater's gateway instead of a reflector.
	repeaterMode := false

	defaultHost := func(rt reflectorType, id string) string {
		// Repeaters have no conventional hostname.
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

	// protoSelect is repeater-only; reflector types fix the protocol.
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

	// The heading is the mode switch; the active side is bold.
	reflectorHeading := widget.NewLabel("Reflector")
	repeaterHeading := widget.NewLabel("Repeater")

	// showMode shows the current mode's rows and refreshes the layout.
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

	// Set once the profile list exists: refreshProfiles rebuilds it for the
	// current mode keeping profile (a Name) selected; selectedName returns the
	// selected profile's Name.
	var refreshProfiles func(profile string)
	var selectedName func() string
	var openProfile func() // loads the selected profile, else the list's first

	// Each mode keeps its own ID, host and selected profile.
	type modeFields struct {
		id, host, profile string
		hostManual        bool
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
		saved[modeIndex(repeaterMode)] = modeFields{idEntry.Text, hostEntry.Text, selectedName(), hostManual}
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
		refreshProfiles(next.profile)
		// Nothing typed or loaded in this mode yet: load its remembered
		// profile (the last used, at startup), else its first one.
		if next.id == "" {
			openProfile()
		}
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

	// shown is the profile list's contents, in Options order.
	var shown []config.ReflectorEntry
	profileSelect := widget.NewSelect(nil, nil)
	profileSelect.PlaceHolder = "(none)"

	// One icon button per sort order; the active one is highlighted.
	sortOrder := ""
	for _, ps := range profileSorts {
		if ps.value == a.cfg.UI.ProfileSort {
			sortOrder = ps.value
		}
	}
	sortValue := func() string { return sortOrder }
	tips := newTipLayer()
	sortButtons := make([]*tipButton, len(profileSorts))
	markSort := func() {
		for i, ps := range profileSorts {
			imp := widget.LowImportance
			if ps.value == sortOrder {
				imp = widget.HighImportance
			}
			if sortButtons[i].Importance != imp {
				sortButtons[i].Importance = imp
				sortButtons[i].Refresh()
			}
		}
	}
	for i, ps := range profileSorts {
		sortButtons[i] = newTipButton(ps.icon, ps.tip, tips, func() {
			if sortOrder == ps.value {
				return
			}
			sortOrder = ps.value
			markSort()
			refreshProfiles(selectedName())
			a.cfg.UI.ProfileSort = sortOrder
			if err := config.Save(a.cfg); err != nil {
				a.appendLog("Save failed: " + err.Error())
			}
		})
	}
	markSort()
	sortBar := container.NewHBox()
	for _, b := range sortButtons {
		sortBar.Add(b)
	}

	selectedName = func() string {
		for _, e := range shown {
			if profileLabel(e) == profileSelect.Selected {
				return e.Name
			}
		}
		return ""
	}
	refreshProfiles = func(profile string) {
		shown = profilesFor(a.cfg.Reflectors, repeaterMode, sortValue())
		profileSelect.Options = make([]string, len(shown))
		profileSelect.Selected = "" // set directly: OnChanged would reload the fields
		for i, e := range shown {
			profileSelect.Options[i] = profileLabel(e)
			if e.Name == profile {
				profileSelect.Selected = profileSelect.Options[i]
			}
		}
		profileSelect.Refresh()
	}
	loadProfile := func(label string) {
		for _, r := range shown {
			if profileLabel(r) != label {
				continue
			}
			if r.RepeaterCall != "" {
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

	// Always start in reflector mode, on LastUsedReflector if it's a
	// reflector, else the first reflector profile. A last-used repeater
	// profile is what repeater mode opens on when switched to.
	openProfile = func() {
		switch {
		case profileSelect.Selected != "":
			profileSelect.SetSelected(profileSelect.Selected) // fires OnChanged to load it
		case len(profileSelect.Options) > 0:
			profileSelect.SetSelected(profileSelect.Options[0])
		}
	}
	refreshProfiles(a.cfg.LastUsedReflector)
	openProfile()
	for _, r := range a.cfg.Reflectors {
		if r.Name == a.cfg.LastUsedReflector && r.RepeaterCall != "" {
			saved[modeIndex(true)].profile = r.Name
		}
	}

	updateHostLink()

	// saveProfile adds e, or replaces the profile with its Name (keeping
	// LastUsed), and selects it.
	saveProfile := func(e config.ReflectorEntry) {
		updated := false
		for i, r := range a.cfg.Reflectors {
			if r.Name == e.Name {
				e.LastUsed = r.LastUsed
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
		refreshProfiles(e.Name)
		if updated {
			a.appendLog("Profile updated: " + profileLabel(e))
		} else {
			a.appendLog("Profile saved: " + profileLabel(e))
		}
	}

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
		// Ask for the friendly name, starting from the saved one when
		// updating, else the link's callsign.
		defName := e.Name
		for _, r := range a.cfg.Reflectors {
			if r.Name == e.Name && r.Label != "" {
				defName = r.Label
			}
		}
		nameEntry := widget.NewEntry()
		nameEntry.SetText(defName)
		d := dialog.NewForm("Save profile", "Save", "Cancel",
			[]*widget.FormItem{widget.NewFormItem("Name", nameEntry)},
			func(ok bool) {
				if !ok {
					return
				}
				e.Label = strings.TrimSpace(nameEntry.Text)
				if e.Label == e.Name {
					e.Label = "" // the default: nothing to show twice
				}
				saveProfile(e)
			}, a.win)
		d.Resize(fyne.NewSize(340, d.MinSize().Height))
		d.Show()
		a.win.Canvas().Focus(nameEntry)
		nameEntry.TypedShortcut(&fyne.ShortcutSelectAll{})
	})

	deleteBtn := widget.NewButton("Delete profile", func() {
		name := selectedName()
		if name == "" {
			return
		}
		label := profileSelect.Selected
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
		refreshProfiles("")
		a.appendLog("Profile deleted: " + label)
	})

	var connected atomic.Bool
	toggleBtn := widget.NewButton("Connect", nil)

	// The mode switch is locked from Connect until Disconnect: the link's
	// mode can't change under it.
	lockMode := func(locked bool) {
		if locked {
			modeSwitch.Disable()
		} else {
			modeSwitch.Enable()
		}
	}

	toggleBtn.OnTapped = func() {
		if connected.Load() {
			toggleBtn.Disable()
			a.disconnect()
			a.closeRadio()
			connected.Store(false)
			toggleBtn.SetText("Connect")
			toggleBtn.Enable()
			lockMode(false)
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
		for i := range a.cfg.Reflectors {
			if a.cfg.Reflectors[i].Name == entry.Name {
				a.cfg.Reflectors[i].LastUsed = time.Now()
				refreshProfiles(selectedName())
				break
			}
		}
		if err := config.Save(a.cfg); err != nil {
			a.appendLog("Config save failed: " + err.Error())
		}

		toggleBtn.Disable()
		lockMode(true)
		go func() {
			a.openRadio(a.cfg.Radio.Port)
			if a.radio == nil || !a.radio.IsOpen() {
				fyne.Do(func() {
					toggleBtn.Enable()
					lockMode(false)
				})
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
		container.NewBorder(nil, nil, container.NewHBox(reflectorHeading, modeSwitch, repeaterHeading), sortBar),
		fields,
		container.NewGridWithColumns(2, saveBtn, deleteBtn),
		toggleBtn,
	)
	return tips.wrap(panel)
}
