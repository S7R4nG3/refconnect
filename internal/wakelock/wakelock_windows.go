//go:build windows

package wakelock

import (
	"fmt"
	"syscall"
)

// SetThreadExecutionState flags.
// See: https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-setthreadexecutionstate
const (
	esContinuous      = 0x80000000
	esSystemRequired  = 0x00000001
	esDisplayRequired = 0x00000002
)

var (
	kernel32                    = syscall.NewLazyDLL("kernel32.dll")
	procSetThreadExecutionState = kernel32.NewProc("SetThreadExecutionState")
)

type winLock struct{}

// Release restores the default system idle timeout by clearing all flags
// except ES_CONTINUOUS.
func (winLock) Release() {
	_, _, _ = procSetThreadExecutionState.Call(uintptr(esContinuous))
}

// Acquire sets ES_CONTINUOUS | ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED
// on the calling thread, which keeps both the system and the display
// awake until Release is called.
func Acquire(reason string) (Lock, error) {
	_ = reason // Windows API takes no reason string
	r1, _, err := procSetThreadExecutionState.Call(
		uintptr(esContinuous | esSystemRequired | esDisplayRequired))
	if r1 == 0 {
		return noopLock{}, fmt.Errorf("wakelock: SetThreadExecutionState: %w", err)
	}
	return winLock{}, nil
}
