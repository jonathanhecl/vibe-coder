//go:build windows

package tui

import "time"

// waitReadable reports whether input is available. Windows consoles offer no
// portable poll for a character device here, so the ask timeout degrades to a
// blocking read instead of risking a half-consumed stdin. ESC still selects the
// default answer because a lone ESC needs no readiness probe.
func waitReadable(fd int, timeout time.Duration) (bool, error) {
	return true, nil
}
