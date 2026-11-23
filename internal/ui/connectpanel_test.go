package ui

import (
	"testing"

	"fyne.io/fyne/v2"
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

// A saved repeater profile opens the panel in repeater mode with its fields.
func TestConnectPanelLoadsRepeaterProfile(t *testing.T) {
	cfg := config.Default()
	cfg.Reflectors = []config.ReflectorEntry{{Name: "W4ABC B", Host: "10.0.0.5", Port: 30001,
		Module: "B", Protocol: "DExtra", RepeaterCall: "W4ABC"}}
	cfg.LastUsedReflector = "W4ABC B"
	_, selects, entries := connectPanelWidgets(t, cfg)
	typeSel, protoSel, modSel := selects[1], selects[2], selects[3]

	if typeSel.Visible() || !protoSel.Visible() {
		t.Error("repeater profile did not open in repeater mode")
	}
	if entries[0].Text != "W4ABC" || entries[1].Text != "10.0.0.5" || modSel.Selected != "B" || protoSel.Selected != "DExtra" {
		t.Errorf("loaded %q %q module %q protocol %q", entries[0].Text, entries[1].Text, modSel.Selected, protoSel.Selected)
	}
}
