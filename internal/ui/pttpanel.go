package ui

import (
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/S7R4nG3/refconnect/internal/radio"
)

const noPortsLabel = "(no ports found)"

const (
	scanInterval = 3 * time.Second
	probeTimeout = 1500 * time.Millisecond
	reprobeAfter = 15 * time.Second
)

// portScanner keeps the port selector in sync with attached radios: it
// labels known radios, selects the most likely DV data port (and sets the
// radio protocol to match, so users never choose it), confirms Icom
// ports with a DV Gateway poll, and picks up radios plugged in later.
type portScanner struct {
	a          *App
	portSelect *widget.Select
	hint       *widget.Label

	// Scan goroutine state, guarded by scanMu.
	scanMu   sync.Mutex
	verified map[string]bool      // ports that answered a probe
	probed   map[string]time.Time // last failed probe per port
	lastLog  string               // port list last written to the log

	// UI-thread state.
	ports      []radio.DetectedPort
	failed     map[string]bool // ports whose last probe got no answer
	userPicked string          // port the user chose by hand this session
	updating   bool            // suppresses OnChanged during programmatic updates
}

// scan enumerates ports, publishes them, then probes unconfirmed Icom ports
// and publishes again if any answered. force discards cached probe results.
func (s *portScanner) scan(force bool) {
	s.scanMu.Lock()
	defer s.scanMu.Unlock()
	if s.a.radioBusy.Load() {
		return
	}
	if force {
		s.verified = map[string]bool{}
		s.probed = map[string]time.Time{}
	}

	ports := radio.DetectPorts()
	present := map[string]bool{}
	for i := range ports {
		present[ports[i].Name] = true
		if s.verified[ports[i].Name] {
			ports[i].MarkVerified()
		}
	}
	// Forget ports that were unplugged so a re-plugged radio is re-probed.
	for name := range s.verified {
		if !present[name] {
			delete(s.verified, name)
		}
	}
	for name := range s.probed {
		if !present[name] {
			delete(s.probed, name)
		}
	}
	radio.SortPorts(ports)
	s.logPorts(ports)
	s.publish(ports)

	changed := false
	for i := range ports {
		p := &ports[i]
		if p.Protocol != radio.ProtocolDVGateway || !p.IsUSB || p.Verified || p.Role == radio.RoleCIV {
			continue
		}
		if t, ok := s.probed[p.Name]; ok && time.Since(t) < reprobeAfter {
			continue
		}
		if s.a.radioBusy.Load() {
			return
		}
		ok, err := radio.ProbeDVGateway(p.Name, probeTimeout)
		switch {
		case err != nil:
			log.Printf("detect: probe %s: %v", p.Name, err)
		case ok:
			log.Printf("detect: probe %s: DV Gateway answered", p.Name)
		default:
			log.Printf("detect: probe %s: no answer", p.Name)
		}
		if ok {
			s.verified[p.Name] = true
			delete(s.probed, p.Name)
			p.MarkVerified()
			changed = true
		} else {
			s.probed[p.Name] = time.Now()
			changed = true // hint changes from "checking" to "not answering"
		}
	}
	if changed {
		radio.SortPorts(ports)
		s.publish(ports)
	}
}

// logPorts writes the detected port list to the log whenever it changes,
// so users can share VID/PID details for radios we don't know yet.
func (s *portScanner) logPorts(ports []radio.DetectedPort) {
	var b strings.Builder
	b.WriteString("ports:") // never equals the initial empty lastLog, so an empty list is logged too
	for _, p := range ports {
		b.WriteString(p.Name + "|" + p.VID + ":" + p.PID + "|" + p.SerialNumber + "\n")
	}
	if b.String() == s.lastLog {
		return
	}
	s.lastLog = b.String()
	log.Printf("detect: %d serial port(s):", len(ports))
	for _, p := range ports {
		log.Printf("detect:   %-32s usb=%v vid:pid=%s:%s serial=%q product=%q model=%q role=%q score=%d",
			p.Name, p.IsUSB, p.VID, p.PID, p.SerialNumber, p.Product, p.Model, p.Role, p.Score)
	}
}

// publish pushes a port list to the widgets on the UI thread.
func (s *portScanner) publish(ports []radio.DetectedPort) {
	ports = append([]radio.DetectedPort(nil), ports...)
	failed := map[string]bool{}
	for name := range s.probed {
		failed[name] = true
	}
	fyne.Do(func() {
		s.ports = ports
		s.failed = failed

		labels := make([]string, len(ports))
		for i, p := range ports {
			labels[i] = p.Label()
		}
		if len(labels) == 0 {
			labels = []string{noPortsLabel}
		}

		i := s.choose()
		if slices.Equal(labels, s.portSelect.Options) && (i < 0 || labels[i] == s.portSelect.Selected) {
			// Nothing visible changed; avoid disturbing an open dropdown.
			s.updateHint()
			return
		}

		s.updating = true
		s.portSelect.Options = labels
		if i >= 0 {
			p := ports[i]
			s.selectPort(p)
			s.portSelect.SetSelected(labels[i])
		} else {
			s.portSelect.ClearSelected()
		}
		s.portSelect.Refresh()
		s.updating = false

		s.updateHint()
	})
}

// choose returns the index in s.ports to select, or -1 if there are none.
// Order: the user's own pick, then a recognised radio (the saved port wins
// a tie), then the saved port, then the first port.
func (s *portScanner) choose() int {
	if len(s.ports) == 0 {
		return -1
	}
	saved := s.a.cfg.Radio.Port
	idx := func(name string) int {
		for i, p := range s.ports {
			if p.Name == name {
				return i
			}
		}
		return -1
	}
	if i := idx(s.userPicked); s.userPicked != "" && i >= 0 {
		return i
	}
	best := s.ports[0]
	if best.IsRadio() {
		if i := idx(saved); i >= 0 && s.ports[i].Score == best.Score {
			return i
		}
		return 0
	}
	if i := idx(saved); i >= 0 {
		return i
	}
	return 0
}

// onPortChanged handles a port picked from the dropdown.
func (s *portScanner) onPortChanged(label string) {
	if s.updating || label == noPortsLabel {
		return
	}
	for _, p := range s.ports {
		if p.Label() == label {
			s.userPicked = p.Name
			s.selectPort(p)
			break
		}
	}
	s.updateHint()
}

// selectPort makes p the radio port. A recognised radio also sets the
// protocol; for anything else the saved protocol is kept.
func (s *portScanner) selectPort(p radio.DetectedPort) {
	s.a.cfg.Radio.Port = p.Name
	if p.Protocol != "" {
		s.a.cfg.Radio.Protocol = p.Protocol
	}
}

// updateHint explains the selected port's state in plain language.
func (s *portScanner) updateHint() {
	var sel *radio.DetectedPort
	for i := range s.ports {
		if s.ports[i].Name == s.a.cfg.Radio.Port {
			sel = &s.ports[i]
		}
	}
	anyRadio := len(s.ports) > 0 && s.ports[0].IsRadio()

	var text string
	switch {
	case !anyRadio:
		text = "No radio detected. Connect your radio with a USB cable and turn it on."
	case sel == nil || !sel.IsRadio():
		text = "A radio is connected, but a different port is selected."
	case sel.Verified:
		text = sel.Model + " is connected and responding."
	case sel.Protocol == radio.ProtocolMMDVM:
		link := "USB"
		if sel.Bluetooth {
			link = "Bluetooth"
		}
		text = sel.Model + " found. Put it in Reflector TERM mode (Menu 650) with Menu 985 set to " + link + " before connecting."
	case sel.Role == radio.RoleCIV:
		text = "This is the radio's CI-V control port. Choose its DV data port instead."
	case s.failed[sel.Name]:
		text = sel.Model + " found but not answering. Put it in DV mode and set its USB data output to DV Data" +
			modelMenuHint(sel.Model) + "."
	default:
		text = "Checking " + sel.Model + "…"
	}
	s.hint.SetText(text)
	s.hint.Show()
}

// modelMenuHint returns the menu path for the DV data setting, if known.
func modelMenuHint(model string) string {
	switch model {
	case "Icom IC-705":
		return " (MENU › SET › Connectors › ACC/USB Output Select)"
	case "Icom IC-9700":
		return " (MENU › SET › Connectors › USB (B) Function)"
	}
	return ""
}

// buildPTTPanel returns the serial port selector. The selection (and the
// protocol of a recognised radio) is written back to a.cfg.Radio immediately
// so that the reflector Connect button can open the radio with current values.
func buildPTTPanel(a *App) fyne.CanvasObject {
	if a.cfg.Radio.Protocol != radio.ProtocolMMDVM {
		a.cfg.Radio.Protocol = radio.ProtocolDVGateway
	}

	hint := widget.NewLabel("")
	hint.Wrapping = fyne.TextWrapWord
	hint.Importance = widget.LowImportance
	hint.Hide()

	s := &portScanner{
		a:        a,
		hint:     hint,
		verified: map[string]bool{},
		probed:   map[string]time.Time{},
	}

	// Start with a placeholder; enumerate ports in the background so
	// the window appears immediately (serial enumeration is slow on Windows).
	s.portSelect = widget.NewSelect([]string{"scanning…"}, s.onPortChanged)

	refreshBtn := widget.NewButtonWithIcon("", theme.ViewRefreshIcon(), func() {
		s.userPicked = ""
		go s.scan(true)
	})

	// Initial scan, then keep watching so a radio plugged in later shows up.
	go func() {
		s.scan(false)
		for range time.Tick(scanInterval) {
			s.scan(false)
		}
	}()

	portRow := container.NewBorder(nil, nil, nil, refreshBtn, s.portSelect)

	helpLink := widget.NewHyperlink("Need help? Check the docs!",
		parseURL("https://github.com/S7R4nG3/refconnect/wiki/Connecting-Radios"))

	aprsCheck := widget.NewCheckWithData("Enable APRS", a.aprsEnabled)

	return container.NewVBox(
		widget.NewLabel("Radio"),
		portRow,
		hint,
		container.NewBorder(nil, nil, aprsCheck, helpLink),
	)
}
