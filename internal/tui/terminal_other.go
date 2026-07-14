//go:build !windows

package tui

import "os"

func configureTerminalForBracketedPaste(_ *os.File, _ *os.File) (func(), bool) {
	return func() {}, true
}
