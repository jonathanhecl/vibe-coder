//go:build windows

package tui

import (
	"bufio"
	"time"
)

// inputWatchable reports whether stdin can be polled with a deadline. The
// Windows console API offers no equivalent of a poll, so the prompt keeps
// waiting for a keypress instead of showing a countdown it cannot honor.
func inputWatchable(int) bool { return false }

// waitByte reads one byte, blocking indefinitely. The countdown is disabled on
// this platform (see inputWatchable), so the deadline is never reached.
func waitByte(_ int, r *bufio.Reader, _ time.Time, _ func(time.Duration) error) (byte, error) {
	return r.ReadByte()
}
