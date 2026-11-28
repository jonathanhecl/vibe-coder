//go:build !windows

package tui

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
	"golang.org/x/term"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
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
		logger.Errorf("prompt capture off: stdin cannot enter non-canonical mode")
		return nil
	}
	capturing := u.capture.available()
	u.capture.setEnabled(capturing)
	if capturing {
		logger.Infof("prompt capture on for this run (draft line active)")
	}
	u.escMu.Lock()
	stop := make(chan struct{})
	done := make(chan struct{})
	u.escCancel = interrupt
	u.escStop = stop
	u.escDone = done
	u.escRestore = restore
	u.escMu.Unlock()

	go u.escMonitorLoop(stop, done)
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
	// The draft line must go before the terminal returns to canonical mode: the
	// composer draws the prompt on that row next.
	u.capture.setEnabled(false)
	if restore != nil {
		restore()
	}
}

// escMonitorLoop reads stdin in non-canonical mode during a run. Every byte goes
// to the prompt capture, which turns it into a draft or a queued prompt and
// reports the double-ESC cancel gesture. It uses unix.Poll with a short timeout
// so it can check the stop channel between reads. The escMu mutex coordinates
// with readSingleChar (AskPermission) so only one reader accesses stdin at a
// time.
func (u *PlainUI) escMonitorLoop(stop <-chan struct{}, done chan struct{}) {
	defer close(done)
	fd := int(u.in.Fd())
	buf := make([]byte, 64)

	for {
		select {
		case <-stop:
			return
		default:
		}

		u.escMu.Lock()
		fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
		n, err := unix.Poll(fds, 50)
		read := 0
		if n > 0 && err == nil {
			read, err = unix.Read(fd, buf)
		}
		u.escMu.Unlock()

		if err != nil || read <= 0 {
			continue
		}
		if u.capture.handle(buf[:read]) {
			fmt.Fprintln(u.out, "\n⏹  Agent stopped (press ESC twice).")
			u.escMu.Lock()
			cancelFn := u.escCancel
			u.escMu.Unlock()
			if cancelFn != nil {
				cancelFn()
			}
			return
		}
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
	termios, err := getTermios(fd)
	if err != nil {
		return nil, false
	}
	old := *termios
	termios.Lflag &^= unix.ICANON | unix.ECHO
	if err := setTermios(fd, termios); err != nil {
		return nil, false
	}
	return func() {
		_ = setTermios(fd, &old)
	}, true
}
