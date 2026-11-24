package ui

import (
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/data/binding"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"

	"github.com/S7R4nG3/refconnect/internal/config"
)

func TestRepeaterEntry(t *testing.T) {
	e, ok := repeaterEntry(" w4abc ", "B", "w4abc.example.net", "DExtra")
	if !ok {
		t.Fatal("valid repeater rejected")
	}
	want := config.ReflectorEntry{Name: "W4ABC B", Host: "w4abc.example.net", Port: 30001,
		Module: "B", Protocol: "DExtra", RepeaterCall: "W4ABC"}
	if e != want {
		t.Errorf("entry = %+v, want %+v", e, want)
	}

	for _, c := range []struct{ call, module, host, proto string }{
		{"", "B", "h", "DPlus"},
		{"W4ABC B", "B", "h", "DPlus"},  // module typed into the callsign
		{"KR4GCQAB", "B", "h", "DPlus"}, // 8 characters leaves no room for the module
		{"W4ABC", "", "h", "DPlus"},
		{"W4ABC", "B", "", "DPlus"},
		{"W4ABC", "B", "h", "XLX"},
	} {
		if _, ok := repeaterEntry(c.call, c.module, c.host, c.proto); ok {
			t.Errorf("repeaterEntry(%q, %q, %q, %q) accepted", c.call, c.module, c.host, c.proto)
		}
	}
}

// Reflector profiles, which have no repeater_call, still parse from their name.
func TestParseEntryReflector(t *testing.T) {
	p, id, _, mod := parseEntry(config.ReflectorEntry{Name: "REF001 C", Host: "ref001.dstargateway.org", Module: "C"})
	if p != "REF" || id != "001" || mod != "C" {
		t.Errorf("reflector parsed as %q %q %q", p, id, mod)
	}
}

// connectPanelWidgets builds the connect panel and returns its widgets in layout order.
func connectPanelWidgets(t *testing.T, cfg *config.Config) (sw *toggleSwitch, selects []*widget.Select, entries []*widget.Entry) {
	t.Helper()
	test.NewTempApp(t)
	a := &App{cfg: cfg, logLines: binding.NewStringList()}
	panel := buildConnectPanel(a)
	w := test.NewTempWindow(t, panel)
	w.Resize(fyne.NewSize(560, 520))

	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch v := o.(type) {
		case *toggleSwitch:
			sw = v
		case *widget.Select:
			selects = append(selects, v)
		case *widget.Entry:
			entries = append(entries, v)
		case *fyne.Container:
			for _, c := range v.Objects {
				walk(c)
			}
		}
	}
	walk(panel)
	return sw, selects, entries
}

// The switch swaps the Type row for Protocol, which must get its own row once
// shown rather than sit at 0,0.
func TestConnectPanelRepeaterSwitch(t *testing.T) {
	sw, selects, entries := connectPanelWidgets(t, config.Default())
	profileSel, typeSel, protoSel := selects[0], selects[1], selects[2]
	idEntry, hostEntry := entries[0], entries[1]
	if protoSel.Options[0] != "DPlus" {
		t.Fatalf("selects[2] is not the protocol select: %v", protoSel.Options)
	}

	if protoSel.Visible() || !typeSel.Visible() {
		t.Fatal("reflector mode should show Type, not Protocol")
	}
	test.Tap(sw)
	if typeSel.Visible() || !protoSel.Visible() || protoSel.Size().Height == 0 {
		t.Fatalf("repeater mode: type visible=%v, protocol visible=%v size=%v",
			typeSel.Visible(), protoSel.Visible(), protoSel.Size())
	}
	if protoSel.Position().Y <= profileSel.Position().Y {
		t.Errorf("protocol row at y=%v, not below profile row at y=%v", protoSel.Position().Y, profileSel.Position().Y)
	}
	if idEntry.Text != "" || hostEntry.Text != "" {
		t.Errorf("repeater fields start as %q / %q, want empty", idEntry.Text, hostEntry.Text)
	}

	// Each mode keeps what was typed in it.
	test.Type(idEntry, "W4ABC")
	test.Type(hostEntry, "w4abc.example.net")
	test.Tap(sw)
	if !typeSel.Visible() || protoSel.Visible() {
		t.Error("switching back did not restore reflector rows")
	}
	if idEntry.Text != "001" || hostEntry.Text != "ref001.dstargateway.org" {
		t.Errorf("reflector fields = %q / %q", idEntry.Text, hostEntry.Text)
	}
	test.Tap(sw)
	if idEntry.Text != "W4ABC" || hostEntry.Text != "w4abc.example.net" {
		t.Errorf("repeater fields = %q / %q", idEntry.Text, hostEntry.Text)
	}
}

// The panel starts in reflector mode even when the last profile used was a
// repeater; switching to repeater mode then opens that profile.
func TestConnectPanelLoadsRepeaterProfile(t *testing.T) {
	cfg := config.Default()
	cfg.Reflectors = []config.ReflectorEntry{{Name: "W4ABC B", Host: "10.0.0.5", Port: 30001,
		Module: "B", Protocol: "DExtra", RepeaterCall: "W4ABC"}}
	cfg.LastUsedReflector = "W4ABC B"
	sw, selects, entries := connectPanelWidgets(t, cfg)
	typeSel, protoSel, modSel := selects[1], selects[2], selects[3]

	if !typeSel.Visible() || protoSel.Visible() || sw.checked {
		t.Fatal("did not start in reflector mode")
	}
	test.Tap(sw)
	if entries[0].Text != "W4ABC" || entries[1].Text != "10.0.0.5" || modSel.Selected != "B" || protoSel.Selected != "DExtra" {
		t.Errorf("loaded %q %q module %q protocol %q", entries[0].Text, entries[1].Text, modSel.Selected, protoSel.Selected)
	}
}

func TestProfilesFor(t *testing.T) {
	now := time.Now()
	all := []config.ReflectorEntry{
		{Name: "XRF030 B", LastUsed: now.Add(-time.Hour)},
		{Name: "W4ABC B", RepeaterCall: "W4ABC"},
		{Name: "REF001 C", Label: "zulu"},
		{Name: "DCS006 A", LastUsed: now},
	}
	names := func(es []config.ReflectorEntry) (out []string) {
		for _, e := range es {
			out = append(out, e.Name)
		}
		return out
	}
	for _, c := range []struct {
		repeater bool
		order    string
		want     []string
	}{
		{false, "", []string{"XRF030 B", "REF001 C", "DCS006 A"}},
		{false, "name", []string{"DCS006 A", "XRF030 B", "REF001 C"}}, // "zulu (REF001 C)" sorts last
		{false, "recent", []string{"DCS006 A", "XRF030 B", "REF001 C"}},
		{true, "", []string{"W4ABC B"}},
	} {
		got := names(profilesFor(all, c.repeater, c.order))
		if len(got) != len(c.want) {
			t.Errorf("repeater=%v order=%q: got %v, want %v", c.repeater, c.order, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("repeater=%v order=%q: got %v, want %v", c.repeater, c.order, got, c.want)
				break
			}
		}
	}
}

func TestProfileLabel(t *testing.T) {
	if got := profileLabel(config.ReflectorEntry{Name: "REF001 C", Label: " Club net "}); got != "Club net (REF001 C)" {
		t.Errorf("labelled = %q", got)
	}
	if got := profileLabel(config.ReflectorEntry{Name: "REF001 C"}); got != "REF001 C" {
		t.Errorf("unlabelled = %q", got)
	}
}

// The profile list shows only the current mode's profiles and keeps each
// mode's selection.
func TestConnectPanelProfilesPerMode(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg := config.Default()
	cfg.Reflectors = []config.ReflectorEntry{
		{Name: "REF001 C", Host: "ref001.dstargateway.org", Port: 20001, Module: "C", Protocol: "DPlus", Label: "Club net"},
		{Name: "W4ABC B", Host: "10.0.0.5", Port: 20001, Module: "B", Protocol: "DPlus", RepeaterCall: "W4ABC", Label: "Home"},
	}
	cfg.LastUsedReflector = "REF001 C"
	sw, selects, entries := connectPanelWidgets(t, cfg)
	profileSel := selects[0]

	if len(profileSel.Options) != 1 || profileSel.Selected != "Club net (REF001 C)" {
		t.Fatalf("reflector mode: options %v selected %q", profileSel.Options, profileSel.Selected)
	}
	test.Tap(sw) // first visit opens the mode's first profile
	if len(profileSel.Options) != 1 || profileSel.Selected != "Home (W4ABC B)" {
		t.Fatalf("repeater mode: options %v selected %q", profileSel.Options, profileSel.Selected)
	}
	if entries[0].Text != "W4ABC" {
		t.Errorf("repeater profile loaded %q", entries[0].Text)
	}
	test.Tap(sw)
	if profileSel.Selected != "Club net (REF001 C)" || entries[0].Text != "001" {
		t.Errorf("back in reflector mode: selected %q id %q", profileSel.Selected, entries[0].Text)
	}
}

// saveDialog taps Save profile and returns the popup's name entry and buttons.
func saveDialog(t *testing.T, w fyne.Window, panel fyne.CanvasObject) (name *widget.Entry, save, cancel *widget.Button) {
	t.Helper()
	var buttons func(fyne.CanvasObject, func(*widget.Button))
	buttons = func(o fyne.CanvasObject, f func(*widget.Button)) {
		if b, ok := o.(*widget.Button); ok {
			f(b)
		}
		if c, ok := o.(*fyne.Container); ok {
			for _, x := range c.Objects {
				buttons(x, f)
			}
		}
	}
	buttons(panel, func(b *widget.Button) {
		if b.Text == "Save profile" {
			test.Tap(b)
		}
	})
	top := w.Canvas().Overlays().Top()
	if top == nil {
		t.Fatal("Save profile did not open a dialog")
	}
	for _, o := range test.LaidOutObjects(top) {
		switch v := o.(type) {
		case *widget.Entry:
			name = v
		case *widget.Button:
			switch v.Text {
			case "Save":
				save = v
			case "Cancel":
				cancel = v
			}
		}
	}
	if name == nil || save == nil || cancel == nil {
		t.Fatalf("dialog widgets: name %v save %v cancel %v", name, save, cancel)
	}
	return name, save, cancel
}

func newTestPanel(t *testing.T, cfg *config.Config) (fyne.Window, fyne.CanvasObject, *widget.Select) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	test.NewTempApp(t)
	a := &App{cfg: cfg, logLines: binding.NewStringList()}
	panel := buildConnectPanel(a)
	a.win = test.NewTempWindow(t, panel)
	a.win.Resize(fyne.NewSize(560, 520))
	var profileSel *widget.Select
	for _, o := range test.LaidOutObjects(panel) {
		if s, ok := o.(*widget.Select); ok && profileSel == nil {
			profileSel = s
		}
	}
	return a.win, panel, profileSel
}

// Save asks for a name, defaulting to the link's callsign, and keeps LastUsed.
func TestConnectPanelSaveLabel(t *testing.T) {
	used := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	cfg := config.Default()
	cfg.Reflectors[0].LastUsed = used
	w, panel, profileSel := newTestPanel(t, cfg)

	name, save, _ := saveDialog(t, w, panel)
	if name.Text != "REF001 C" {
		t.Errorf("default name = %q, want the callsign", name.Text)
	}
	test.Tap(save) // keeping the default stores no label
	if cfg.Reflectors[0].Label != "" {
		t.Errorf("default name stored as label %q", cfg.Reflectors[0].Label)
	}

	name, save, _ = saveDialog(t, w, panel)
	name.SetText("Morning net")
	test.Tap(save)
	got := cfg.Reflectors[0]
	if len(cfg.Reflectors) != 1 || got.Label != "Morning net" || !got.LastUsed.Equal(used) {
		t.Errorf("saved %+v", cfg.Reflectors)
	}
	if profileSel.Selected != "Morning net (REF001 C)" || len(profileSel.Options) != 1 {
		t.Errorf("list after save: options %v selected %q", profileSel.Options, profileSel.Selected)
	}

	// Re-saving starts from the saved name; Cancel leaves the profile alone.
	name, _, cancel := saveDialog(t, w, panel)
	if name.Text != "Morning net" {
		t.Errorf("re-save default = %q", name.Text)
	}
	name.SetText("Other")
	test.Tap(cancel)
	if cfg.Reflectors[0].Label != "Morning net" {
		t.Errorf("cancel changed label to %q", cfg.Reflectors[0].Label)
	}
}

// The sort buttons reorder the list, highlight the active order and save it.
func TestConnectPanelSortButtons(t *testing.T) {
	cfg := config.Default()
	cfg.Reflectors = []config.ReflectorEntry{
		{Name: "XRF030 B", Host: "xrf030.example.net", Port: 30001, Module: "B", Protocol: "DExtra"},
		{Name: "REF001 C", Host: "ref001.dstargateway.org", Port: 20001, Module: "C", Protocol: "DPlus",
			LastUsed: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)},
	}
	_, panel, profileSel := newTestPanel(t, cfg)
	var buttons []*tipButton
	for _, o := range test.LaidOutObjects(panel) {
		if b, ok := o.(*tipButton); ok {
			buttons = append(buttons, b)
		}
	}
	if len(buttons) != len(profileSorts) {
		t.Fatalf("found %d sort buttons", len(buttons))
	}
	for i, c := range []struct {
		order string
		want  string // first option
	}{{"", "XRF030 B"}, {"name", "REF001 C"}, {"recent", "REF001 C"}} {
		if c.order != "" {
			test.Tap(buttons[i])
		}
		if profileSel.Options[0] != c.want || cfg.UI.ProfileSort != c.order {
			t.Errorf("order %q: options %v, saved sort %q", c.order, profileSel.Options, cfg.UI.ProfileSort)
		}
		for j, b := range buttons {
			if (b.Importance == widget.HighImportance) != (i == j) {
				t.Errorf("order %q: button %d importance %v", c.order, j, b.Importance)
			}
		}
	}
	test.Tap(buttons[0])
	if profileSel.Options[0] != "XRF030 B" || cfg.UI.ProfileSort != "" {
		t.Errorf("back to saved order: %v / %q", profileSel.Options, cfg.UI.ProfileSort)
	}
}

// Hovering a sort button shows its tip below it, inside the panel; leaving hides it.
func TestSortButtonTooltip(t *testing.T) {
	tipDelay = 0
	t.Cleanup(func() { tipDelay = 500 * time.Millisecond })
	w, panel, _ := newTestPanel(t, config.Default())
	var btn *tipButton
	var tip *canvas.Text
	for _, o := range test.LaidOutObjects(panel) {
		switch v := o.(type) {
		case *tipButton:
			btn = v // the last one: right-most, nearest the edge
		case *canvas.Text:
			tip = v
		}
	}
	d := fyne.CurrentApp().Driver()
	at := d.AbsolutePositionForObject(btn)
	test.MoveMouse(w.Canvas(), at.Add(fyne.NewPos(4, 4)))
	if tip.Text != profileSorts[2].tip || !btn.layer.bubble.Visible() {
		t.Fatalf("hover: tip %q visible %v", tip.Text, btn.layer.bubble.Visible())
	}
	tp := d.AbsolutePositionForObject(tip)
	if tp.Y <= at.Y+btn.Size().Height-1 || tp.X+tip.Size().Width > w.Canvas().Size().Width {
		t.Errorf("tip at %v size %v, button at %v size %v, canvas %v", tp, tip.Size(), at, btn.Size(), w.Canvas().Size())
	}
	test.MoveMouse(w.Canvas(), fyne.NewPos(5, 300))
	if btn.layer.bubble.Visible() {
		t.Error("tip still shown after the pointer left")
	}
}

// With several profiles, reflector mode opens on the last used reflector and
// repeater mode on the last used repeater, not its first.
func TestConnectPanelOpensLastUsedPerMode(t *testing.T) {
	cfg := config.Default()
	rpt := func(call string) config.ReflectorEntry {
		return config.ReflectorEntry{Name: call + " B", Host: "10.0.0.5", Port: 20001, Module: "B", Protocol: "DPlus", RepeaterCall: call}
	}
	cfg.Reflectors = append(cfg.Reflectors,
		config.ReflectorEntry{Name: "XRF030 B", Host: "xrf030.example.net", Port: 30001, Module: "B", Protocol: "DExtra"},
		rpt("W4ABC"), rpt("W4XYZ"))
	cfg.LastUsedReflector = "W4XYZ B"
	sw, selects, entries := connectPanelWidgets(t, cfg)
	if selects[0].Selected != "REF001 C" || entries[0].Text != "001" || entries[1].Text != "ref001.dstargateway.org" {
		t.Fatalf("reflector start: selected %q id %q host %q", selects[0].Selected, entries[0].Text, entries[1].Text)
	}
	test.Tap(sw)
	if selects[0].Selected != "W4XYZ B" || entries[0].Text != "W4XYZ" {
		t.Errorf("repeater mode: selected %q id %q", selects[0].Selected, entries[0].Text)
	}

	cfg.LastUsedReflector = "XRF030 B"
	_, selects, entries = connectPanelWidgets(t, cfg)
	if selects[0].Selected != "XRF030 B" || entries[0].Text != "030" {
		t.Errorf("last used reflector: selected %q id %q", selects[0].Selected, entries[0].Text)
	}
}

// A disabled mode switch ignores taps and leaves the mode alone.
func TestModeSwitchLocked(t *testing.T) {
	sw, selects, _ := connectPanelWidgets(t, config.Default())
	sw.Disable()
	test.Tap(sw)
	if sw.checked || !selects[1].Visible() {
		t.Error("disabled switch changed the mode")
	}
	sw.Enable()
	test.Tap(sw)
	if !sw.checked || selects[1].Visible() {
		t.Error("re-enabled switch did not change the mode")
	}
}
