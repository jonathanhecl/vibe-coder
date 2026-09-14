//go:build !windows

package tui

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/unix"
	"golang.org/x/term"
)

// StartESCMonitor enables double-ESC detection during agent execution. It puts
// stdin into non-canonical mode (immediate byte delivery, no echo, but Ctrl+C
// signals and output formatting still work) and starts a goroutine that reads
// key presses. Two ESC keys within 500 ms call the interrupt function, which
// cancels the agent's context and returns the user to a fresh prompt.
func (u *PlainUI) StartESCMonitor(interrupt func()) error {
	if u.in == nil || !term.IsTerminal(int(u.in.Fd())) {
		return nil
	}
	restore, ok := setNonCanonical(u.in)
	if !ok {
		return nil
	}
	u.escMu.Lock()
	u.escCancel = interrupt
	u.escStop = make(chan struct{})
	u.escDone = make(chan struct{})
	u.escRestore = restore
	u.escMu.Unlock()

	go u.escMonitorLoop()
	return nil
}

// StopESCMonitor stops the ESC monitor goroutine and restores the terminal.
// Safe to call even if StartESCMonitor was a no-op (no TTY, non-Windows, etc.).
func (u *PlainUI) StopESCMonitor() {
	u.escMu.Lock()
	stop := u.escStop
	done := u.escDone
	restore := u.escRestore
	u.escStop = nil
	u.escDone = nil
	u.escCancel = nil
	u.escRestore = nil
	u.escMu.Unlock()

	if stop == nil {
		return
	}
	close(stop)
	<-done
	if restore != nil {
		restore()
	}
}

// escMonitorLoop reads stdin in non-canonical mode, watching for two ESC
// (0x1b) bytes within 500 ms. It uses unix.Poll with a short timeout so it can
// check the stop channel between reads. The escMu mutex coordinates with
// readSingleChar (AskPermission) so only one reader accesses stdin at a time.
func (u *PlainUI) escMonitorLoop() {
	defer close(u.escDone)
	fd := int(u.in.Fd())
	var lastEsc time.Time
	buf := make([]byte, 1)

	for {
		select {
		case <-u.escStop:
			return
		default:
		}

		u.escMu.Lock()
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 50)
		if n > 0 && err == nil {
			_, err := unix.Read(fd, buf)
			if err == nil && buf[0] == 0x1b {
				now := time.Now()
				if now.Sub(lastEsc) < 500*time.Millisecond {
					fmt.Fprintln(u.out, "\n⏹  Agent stopped (press ESC twice).")
					if u.escCancel != nil {
						u.escCancel()
					}
					u.escMu.Unlock()
					return
				}
				lastEsc = now
			}
		}
		u.escMu.Unlock()
	}
}

// setNonCanonical puts the terminal into non-canonical mode: input bytes are
// delivered immediately (no line buffering) and echo is disabled, but signal
// generation (ISIG, so Ctrl+C still works) and output processing (OPOST/ONLCR,
// so \n is still translated to \r\n) are preserved. This lets the ESC monitor
// detect individual key presses during agent execution without breaking
// formatted output or the Ctrl+C signal handler.
func setNonCanonical(f *os.File) (restore func(), ok bool) {
	fd := int(f.Fd())
	termios, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return nil, false
	}
	old := *termios
	termios.Lflag &^= unix.ICANON | unix.ECHO
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, termios); err != nil {
		return nil, false
	}
	return func() {
		_ = unix.IoctlSetTermios(fd, unix.TCSETS, &old)
	}, true
}
