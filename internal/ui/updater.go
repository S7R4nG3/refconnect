package ui

import (
	"context"
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/S7R4nG3/refconnect/internal/update"
)

// checkForUpdate asks GitHub for the latest release and, if it is newer than
// the running version, offers to install it. Safe to call from any goroutine.
func (a *App) checkForUpdate() {
	if a.version == "" {
		return
	}
	latest, newer, err := update.CheckLatest(context.Background(), a.version)
	if err != nil {
		a.appendLog("Update check failed: " + err.Error())
		return
	}
	if !newer {
		return
	}
	a.appendLog(fmt.Sprintf("Update available: %s (running %s)", latest, a.version))

	if update.ManagedInstall() {
		a.appendLog("Managed install detected — update via fresh download.")
		msg := fmt.Sprintf("RefConnect %s is available (you have %s).\n\n"+
			"You installed via a system package (.deb/.rpm/AUR) or AppImage, so the in-app updater is disabled.\n"+
			"Download the new release from GitHub and install it:\n"+
			"  Debian/Ubuntu: download refconnect*.deb, then sudo apt install ./refconnect*.deb\n"+
			"  Fedora/RHEL: download refconnect*.rpm, then sudo dnf install ./refconnect*.rpm\n"+
			"  Arch (AUR): update the refconnect-bin or refconnect-git package with your AUR helper.\n"+
			"  AppImage: download the new file from GitHub Releases and make it executable.",
			latest, a.version)
		fyne.Do(func() {
			dialog.ShowInformation("Update Available", msg, a.win)
		})
		return
	}
	fyne.Do(func() {
		msg := fmt.Sprintf("RefConnect %s is available (you have %s).\n\n"+
			"Update now? RefConnect will restart when the update is installed.", latest, a.version)
		dialog.ShowConfirm("Update Available", msg, func(ok bool) {
			if ok {
				a.installUpdate(latest)
			}
		}, a.win)
	})
}

// installUpdate runs the setup script in the background behind a progress
// dialog, then relaunches the new version and quits. Must be called on the
// UI goroutine.
func (a *App) installUpdate(latest string) {
	progress := dialog.NewCustomWithoutButtons("Updating", container.NewVBox(
		widget.NewLabel("Installing RefConnect "+latest+"…"),
		widget.NewProgressBarInfinite(),
	), a.win)
	progress.Show()

	go func() {
		a.appendLog("Installing update " + latest + "…")
		relaunch, err := update.Install(context.Background(), func(format string, args ...any) {
			a.appendLog(fmt.Sprintf(format, args...))
		})
		if err != nil {
			a.appendLog("Update failed: " + err.Error())
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowError(fmt.Errorf("update failed: %w", err), a.win)
			})
			return
		}

		// Release the radio and reflector before the new instance starts so
		// it can open the serial port.
		a.shutdown()
		if err := update.Relaunch(relaunch); err != nil {
			a.appendLog("Relaunch failed: " + err.Error())
			fyne.Do(func() {
				progress.Hide()
				dialog.ShowInformation("Update Installed",
					"RefConnect "+latest+" was installed but could not be restarted automatically.\n"+
						"Please quit and reopen RefConnect.", a.win)
			})
			return
		}
		fyne.Do(func() { a.fyneApp.Quit() })
	}()
}
