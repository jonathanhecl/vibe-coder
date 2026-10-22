//go:build !windows

package tui

import (
	"errors"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// waitReadable reports whether fd has input available within timeout. A
// negative timeout waits indefinitely; false means the timeout elapsed.
func waitReadable(fd int, timeout time.Duration) (bool, error) {
	ms := -1
	if timeout >= 0 {
		ms = int(timeout / time.Millisecond)
		if ms < 0 {
			ms = 0
		}
	}
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, ms)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return false, err
		}
		return n > 0, nil
	}
}
