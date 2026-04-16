// Package ui implements the Fyne-based graphical user interface.
package ui

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/data/binding"

	"github.com/S7R4nG3/refconnect/internal/aprs"
	"github.com/S7R4nG3/refconnect/internal/config"
	"github.com/S7R4nG3/refconnect/internal/ircddb"
	"github.com/S7R4nG3/refconnect/internal/protocol"
	"github.com/S7R4nG3/refconnect/internal/protocol/dextra"
	"github.com/S7R4nG3/refconnect/internal/protocol/dplus"
	"github.com/S7R4nG3/refconnect/internal/protocol/xlx"
	"github.com/S7R4nG3/refconnect/internal/radio"
	"github.com/S7R4nG3/refconnect/internal/router"
	"github.com/S7R4nG3/refconnect/internal/wakelock"
)

// App is the top-level application controller.
type App struct {
	fyneApp fyne.App
	win     fyne.Window
	cfg     *config.Config

	radio           radio.RadioInterface
	reflector       protocol.Reflector
	reflectorModule byte   // target module on the reflector (e.g. 'C')
	reflectorCall   string // reflector D-STAR callsign (e.g. "REF001")
	rt              *router.Router
	irc             *ircddb.Client

	// Shared data bindings updated from any goroutine, consumed by widgets.
	statusText  binding.String
	logLines    binding.StringList
	rxActive    binding.Bool
	txActive    binding.Bool
	linkState   binding.String
	lastHeard   binding.String
	aprsEnabled binding.Bool

	// APRS beacon lifecycle. The ticker runs while a reflector is connected
	// and aprsEnabled is true; stopAPRSCh signals it to exit.
	aprsMu      sync.Mutex
	stopAPRSCh  chan struct{}

	// wake prevents the host from sleeping while a reflector link is up.
	// Held between connect() and disconnect().
	wake wakelock.Lock
}

// Run initialises the application and blocks until the window is closed.
func Run(cfg *config.Config) {
	a := &App{
		cfg:         cfg,
		statusText:  binding.NewString(),
		logLines:    binding.NewStringList(),
		rxActive:    binding.NewBool(),
		txActive:    binding.NewBool(),
		linkState:   binding.NewString(),
		lastHeard:   binding.NewString(),
		aprsEnabled: binding.NewBool(),
	}
	a.statusText.Set("Disconnected")     //nolint:errcheck
	a.linkState.Set("Disconnected")      //nolint:errcheck
	a.aprsEnabled.Set(cfg.APRS.Enabled) //nolint:errcheck
	a.aprsEnabled.AddListener(binding.NewDataListener(func() {
		on, _ := a.aprsEnabled.Get()
		a.cfg.APRS.Enabled = on
		if on {
			a.startAPRS()
		} else {
			a.stopAPRS()
		}
	}))

	a.fyneApp = app.NewWithID("org.refconnect.refconnect")

	a.fyneApp.Settings().SetTheme(&refconnectTheme{})

	a.win = a.fyneApp.NewWindow("RefConnect — D-STAR Reflector Client")
	a.win.SetIcon(resourceAntennaPng)
	a.win.SetContent(buildMainWindow(a))
	// Resize must be called after SetContent so Fyne can reconcile the
	// requested size against the content's minimum size.
	a.win.Resize(fyne.NewSize(cfg.UI.WindowWidth, 0))
	a.win.SetMaster()
	a.win.SetOnClosed(func() {
		a.shutdown()
	})

	a.win.ShowAndRun()
}

// appendLog adds a timestamped line to the log binding (safe from any goroutine).
func (a *App) appendLog(msg string) {
	line := fmt.Sprintf("[%s] %s", time.Now().Format("15:04:05"), msg)
	lines, _ := a.logLines.Get()
	maxLines := a.cfg.UI.LogMaxLines
	if maxLines <= 0 {
		maxLines = 500
	}
	lines = append([]string{line}, lines...)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
	}
	a.logLines.Set(lines) //nolint:errcheck
}

// connect establishes a reflector link using the current UI settings.
func (a *App) connect(entry config.ReflectorEntry) {
	var ref protocol.Reflector
	switch protocol.Protocol(entry.Protocol) {
	case protocol.ProtoDExtra:
		ref = dextra.New()
	case protocol.ProtoDPlus:
		ref = dplus.New()
	case protocol.ProtoXLX:
		ref = xlx.New()
	default:
		a.appendLog("Unknown protocol: " + entry.Protocol)
		return
	}
	a.reflector = ref

	myCall := buildMyCall(a.cfg.Callsign, a.cfg.CallsignSuffix)

	// Restart ircDDB if the callsign changed so the new callsign gets registered.
	ircRestarted := false
	if a.irc == nil || a.irc.Nick() != ircddb.New(myCall).Nick() {
		if a.irc != nil {
			a.irc.Stop()
		}
		a.irc = ircddb.New(myCall)
		a.irc.Start()
		ircRestarted = true
		go func() {
			for evt := range a.irc.Events() {
				a.appendLog("ircDDB: " + evt.Message)
			}
		}()
	}

	a.reflectorModule = entry.Module[0]
	// Extract reflector callsign from entry name (e.g. "REF001 C" → "REF001").
	a.reflectorCall = strings.TrimRight(strings.TrimSuffix(entry.Name, " "+entry.Module), " ")

	cfg := protocol.Config{
		Host:     entry.Host,
		Port:     entry.Port,
		Module:   entry.Module[0],
		MyCall:   myCall,
		Protocol: protocol.Protocol(entry.Protocol),
	}

	a.appendLog(fmt.Sprintf("Connecting to %s (%s:%d module %s) via %s…",
		entry.Name, entry.Host, entry.Port, entry.Module, entry.Protocol))
	a.linkState.Set("Connecting…") //nolint:errcheck

	irc := a.irc
	go func() {
		if ircRestarted && irc != nil {
			if !irc.WaitRegistered(30 * time.Second) {
				log.Printf("connect: ircDDB did not register within 30s, proceeding anyway")
			}
		}
		if err := ref.Connect(cfg); err != nil {
			log.Printf("connect: failed to %s: %v", entry.Host, err)
			a.appendLog("Connect failed: " + err.Error())
			a.linkState.Set("Error: " + err.Error()) //nolint:errcheck
			return
		}
		log.Printf("connect: linked to %s", entry.Host)
		a.appendLog("Linked to " + entry.Name)
		a.linkState.Set("Connected — " + entry.Name) //nolint:errcheck

		// Keep the host awake for the duration of the link so the OS
		// doesn't idle-sleep and drop reflector keepalives.
		if a.wake == nil {
			wl, err := wakelock.Acquire("RefConnect linked to " + entry.Name)
			if err != nil {
				a.appendLog("Wakelock unavailable: " + err.Error())
			}
			a.wake = wl
		}

		// If a radio is already open, start routing.
		if a.radio != nil && a.radio.IsOpen() {
			a.startRouter()
		}

		// Start the APRS beacon loop if the user has it enabled.
		if a.cfg.APRS.Enabled {
			a.startAPRS()
		}

		// Forward reflector events to the log.
		go func() {
			for evt := range ref.Events() {
				a.appendLog(evt.Message)
				a.linkState.Set(evt.State.String()) //nolint:errcheck
			}
		}()
	}()
}

// disconnect gracefully unlinks from the current reflector and de-registers from ircDDB.
func (a *App) disconnect() {
	a.stopAPRS()
	if a.rt != nil {
		a.rt.Stop()
		a.rt = nil
	}
	if a.reflector != nil {
		if err := a.reflector.Disconnect(); err != nil {
			a.appendLog("Disconnect error: " + err.Error())
		}
		a.reflector = nil
	}
	if a.irc != nil {
		a.irc.Stop()
		a.irc = nil
	}
	if a.wake != nil {
		a.wake.Release()
		a.wake = nil
	}
	a.linkState.Set("Disconnected") //nolint:errcheck
	a.appendLog("Disconnected.")
}

// startAPRS begins a goroutine that sends a DPRS beacon on connect (if
// configured) and thereafter on a ticker. It is idempotent.
func (a *App) startAPRS() {
	a.aprsMu.Lock()
	defer a.aprsMu.Unlock()
	if a.stopAPRSCh != nil {
		return // already running
	}
	stop := make(chan struct{})
	a.stopAPRSCh = stop

	interval := time.Duration(a.cfg.APRS.BeaconIntervalMinutes) * time.Minute
	if interval <= 0 {
		interval = 30 * time.Minute
	}
	sendOnConnect := a.cfg.APRS.SendOnConnect

	go func() {
		if sendOnConnect {
			// Delay slightly so the link settles before the first beacon.
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Second):
				a.sendBeacon()
			}
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				a.sendBeacon()
			}
		}
	}()
	a.appendLog("APRS beacon enabled.")
}

// stopAPRS halts the beacon goroutine if it is running.
func (a *App) stopAPRS() {
	a.aprsMu.Lock()
	defer a.aprsMu.Unlock()
	if a.stopAPRSCh == nil {
		return
	}
	close(a.stopAPRSCh)
	a.stopAPRSCh = nil
}

// sendBeacon constructs a DPRS position packet from the current GPS cache
// and transmits it via the router. It is a no-op if the router is not
// connected or we have no GPS fix yet.
func (a *App) sendBeacon() {
	if a.rt == nil {
		return
	}
	pos, _, ok := a.rt.GPS().Get()
	if !ok {
		a.appendLog("APRS: no GPS fix yet — transmit once to cache a position.")
		return
	}
	call := strings.ToUpper(strings.TrimSpace(a.cfg.Callsign))
	symTable := byte('/')
	if len(a.cfg.APRS.SymbolTable) > 0 {
		symTable = a.cfg.APRS.SymbolTable[0]
	}
	symChar := byte('>')
	if len(a.cfg.APRS.Symbol) > 0 {
		symChar = a.cfg.APRS.Symbol[0]
	}
	tnc2 := aprs.BuildPositionPacket(call, pos, symTable, symChar, a.cfg.APRS.Comment)
	sentence := aprs.WrapDPRS(tnc2)
	if err := a.rt.SendBeacon(sentence); err != nil {
		a.appendLog("APRS beacon failed: " + err.Error())
		return
	}
	a.appendLog(fmt.Sprintf("APRS beacon sent (%.5f, %.5f).", pos.Lat, pos.Lon))
}

// openRadio opens the serial port for the radio.
func (a *App) openRadio(portName string) {
	var r radio.RadioInterface
	if a.cfg.Radio.Protocol == "MMDVM" {
		r = radio.NewMMDVMRadio()
	} else {
		r = radio.NewSerialRadio()
	}
	cfg := radio.Config{
		Port: portName,
	}
	if err := r.Open(cfg); err != nil {
		a.appendLog("Radio open error: " + err.Error())
		return
	}
	a.radio = r
	a.appendLog("Radio opened on " + portName)

	if a.reflector != nil && a.reflector.State() == protocol.StateConnected {
		a.startRouter()
	}
}

// closeRadio releases the serial port.
func (a *App) closeRadio() {
	if a.rt != nil {
		a.rt.Stop()
		a.rt = nil
	}
	if a.radio != nil {
		a.radio.Close() //nolint:errcheck
		a.radio = nil
		a.appendLog("Radio closed.")
	}
}

// ptt asserts or releases PTT.
func (a *App) ptt(on bool) {
	if a.radio == nil || !a.radio.IsOpen() {
		return
	}
	if err := a.radio.PTT(on); err != nil {
		a.appendLog("PTT error: " + err.Error())
	}
	a.txActive.Set(on) //nolint:errcheck
}

// startRouter wires up the router between the open radio and reflector.
func (a *App) startRouter() {
	if a.rt != nil {
		a.rt.Stop()
	}
	myCall := buildMyCall(a.cfg.Callsign, a.cfg.CallsignSuffix)
	rt := router.New(a.radio, a.reflector, router.Config{
		DropTXWhenDisconnected: true,
		MyCall:                 myCall,
		ReflectorModule:        a.reflectorModule,
		ReflectorCall:          a.reflectorCall,
	})
	a.rt = rt
	rt.Start()

	go func() {
		for evt := range rt.Events() {
			if evt.Err != nil {
				a.appendLog("Router error: " + evt.Err.Error())
				continue
			}
			if evt.Header != nil {
				who := evt.Header.MyCall
				a.lastHeard.Set(who) //nolint:errcheck
				dir := "RX"
				if evt.Direction == router.DirTX {
					dir = "TX"
					if a.irc != nil {
						a.irc.AnnounceUser(*evt.Header)
					}
				}
				a.appendLog(fmt.Sprintf("%s header: %s → %s", dir, who, evt.Header.YourCall))
				if evt.Direction == router.DirRX {
					a.rxActive.Set(true) //nolint:errcheck
				} else {
					a.txActive.Set(true) //nolint:errcheck
				}
			}
			if evt.Frame != nil && evt.Frame.End {
				if evt.Direction == router.DirRX {
					a.rxActive.Set(false) //nolint:errcheck
				} else {
					a.txActive.Set(false) //nolint:errcheck
				}
			}
		}
	}()
}

func (a *App) shutdown() {
	a.disconnect()
	a.closeRadio()
	if a.irc != nil {
		a.irc.Stop()
	}
	if err := config.Save(a.cfg); err != nil {
		_ = err // best-effort save on exit
	}
}

// buildMyCall constructs the full 8-char D-STAR gateway callsign from the
// stored base callsign and suffix letter (e.g. "KR4GCQ" + "D" → "KR4GCQ D").
func buildMyCall(callsign, suffix string) string {
	s := strings.ToUpper(suffix)
	if len(s) != 1 || (s[0] != ' ' && (s[0] < 'A' || s[0] > 'Z')) {
		s = " "
	}
	base := strings.ToUpper(strings.TrimSpace(callsign))
	if len(base) > 7 {
		base = base[:7]
	}
	return fmt.Sprintf("%-7s%s", base, s)
}
