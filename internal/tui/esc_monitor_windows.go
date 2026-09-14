//go:build windows

package tui

import "os"

// StartESCMonitor is a no-op on Windows where the console mode API does not
// map cleanly to the Unix non-canonical concept. Double-ESC detection is
// unavailable on Windows; Ctrl+C remains the cancellation mechanism.
func (u *PlainUI) StartESCMonitor(interrupt func()) error { return nil }

// StopESCMonitor is a no-op on Windows.
func (u *PlainUI) StopESCMonitor() {}

// setNonCanonical is a no-op on Windows.
func setNonCanonical(_ *os.File) (restore func(), ok bool) {
	return nil, false
}
