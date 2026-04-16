//go:build linux

package wakelock

import (
	"fmt"
	"os/exec"
)

// processLock holds a spawned `systemd-inhibit` process. The inhibitor is
// released automatically when the child process exits, so killing the
// child relinquishes the lock.
type processLock struct {
	cmd *exec.Cmd
}

func (l *processLock) Release() {
	if l == nil || l.cmd == nil || l.cmd.Process == nil {
		return
	}
	_ = l.cmd.Process.Kill()
	_, _ = l.cmd.Process.Wait()
}

// Acquire runs `systemd-inhibit --what=idle:sleep --why=<reason> sleep infinity`.
// On systems without systemd (e.g. minimal Alpine, Void), the command will
// fail to start; the error is returned and the caller gets a no-op Lock.
func Acquire(reason string) (Lock, error) {
	cmd := exec.Command("systemd-inhibit",
		"--what=idle:sleep",
		"--why="+reason,
		"--who=RefConnect",
		"--mode=block",
		"sleep", "infinity")
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return noopLock{}, fmt.Errorf("wakelock: systemd-inhibit: %w", err)
	}
	return &processLock{cmd: cmd}, nil
}
