//go:build linux

package tui

import "golang.org/x/sys/unix"

// getTermios/setTermios abstract the platform-specific ioctl request codes used
// to read and write terminal attributes. Linux names them TCGETS/TCSETS while
// the BSDs (including macOS) use TIOCGETA/TIOCSETA.
func getTermios(fd int) (*unix.Termios, error) {
	return unix.IoctlGetTermios(fd, unix.TCGETS)
}

func setTermios(fd int, t *unix.Termios) error {
	return unix.IoctlSetTermios(fd, unix.TCSETS, t)
}
