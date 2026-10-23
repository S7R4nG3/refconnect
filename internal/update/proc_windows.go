package update

import (
	"os/exec"
	"syscall"
)

// createNoWindow keeps PowerShell from flashing a console window, since the
// release build is a GUI-subsystem executable with no console of its own.
const createNoWindow = 0x08000000

func hideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: createNoWindow}
}
