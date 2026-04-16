//go:build darwin

package wakelock

import (
	"fmt"
	"os/exec"
)

// processLock holds a spawned `caffeinate` process whose lifetime mirrors
// the wakelock. Killing the process releases the assertion.
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

// Acquire runs `caffeinate -disu` which prevents display sleep (-d),
// idle sleep (-i), system sleep (-s), and marks the user as active (-u).
// The process runs in the background for the lifetime of the Lock.
func Acquire(reason string) (Lock, error) {
	_ = reason // caffeinate has no reason argument
	cmd := exec.Command("caffeinate", "-disu")
	// Detach IO so caffeinate doesn't interact with our terminal.
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return noopLock{}, fmt.Errorf("wakelock: caffeinate: %w", err)
	}
	return &processLock{cmd: cmd}, nil
}
