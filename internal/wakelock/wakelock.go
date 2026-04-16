// Package wakelock prevents the host operating system from sleeping while
// RefConnect is actively connected to a reflector. Losing the OS to sleep
// drops the UDP keepalives to the reflector and breaks the link, so we
// acquire an OS-level power-management inhibitor for the duration of a
// connection.
//
// The implementation is platform-specific:
//
//   - macOS: spawns `caffeinate -disu`
//   - Linux: spawns `systemd-inhibit --what=idle:sleep sleep infinity`
//   - Windows: calls SetThreadExecutionState
//
// Failures are non-fatal — Acquire returns a Lock whose Release is a
// no-op, allowing the app to run without a wakelock on systems that do
// not have the required utilities.
package wakelock

// Lock represents an active OS power-management inhibitor.
// Release must be called to relinquish it.
type Lock interface {
	Release()
}

// noopLock is returned when a platform has no wakelock support or the
// acquisition failed. It makes the caller's lifecycle code simpler because
// Release is always safe to call.
type noopLock struct{}

func (noopLock) Release() {}
