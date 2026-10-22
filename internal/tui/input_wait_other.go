//go:build !windows

package tui

import (
	"bufio"
	"errors"
	"time"

	"golang.org/x/sys/unix"
)

// inputWatchable reports whether stdin can be polled with a deadline. Unix
// terminals answer a poll, so the countdown works there.
func inputWatchable(int) bool { return true }

// waitByte returns the next byte from r, or errPromptTimeout when the deadline
// passes first. It polls the descriptor instead of blocking on a read so the
// caller can repaint a countdown between polls; once poll reports data, the byte
// is already there and ReadByte does not block. An error returned by tick stops
// the wait and comes back unchanged, which is how a caller that has to redraw
// more than one line (a live prompt with a buffer behind it) takes control.
func waitByte(fd int, r *bufio.Reader, deadline time.Time, tick func(time.Duration) error) (byte, error) {
	for {
		if r.Buffered() > 0 {
			return r.ReadByte()
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return 0, errPromptTimeout
		}
		step := time.Second
		if remaining < step {
			step = remaining
		}
		// Round the wait up: a sub-millisecond remainder must not become a
		// zero-millisecond poll, which would spin until the deadline.
		ms := int((step + time.Millisecond - 1) / time.Millisecond)
		pfds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(pfds, ms)
		if err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return 0, err
		}
		if n > 0 {
			// Readable, hung up or errored: either way ReadByte returns now
			// instead of blocking, and the caller turns an error into a denial.
			return r.ReadByte()
		}
		// The deadline wins over a repaint: once the wait is over there is
		// nothing left to count down, and ticking first would let a caller draw
		// one last state for a prompt that is already gone.
		if time.Until(deadline) <= 0 {
			return 0, errPromptTimeout
		}
		if tick != nil {
			if err := tick(time.Until(deadline)); err != nil {
				return 0, err
			}
		}
	}
}
