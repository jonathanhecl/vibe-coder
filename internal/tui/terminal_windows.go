//go:build windows

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

func configureTerminalForBracketedPaste(_ *os.File, out *os.File) (func(), bool) {
	outputHandle := windows.Handle(out.Fd())

	var outputMode uint32
	if windows.GetConsoleMode(outputHandle, &outputMode) != nil {
		return nil, false
	}
	if windows.SetConsoleMode(outputHandle, outputMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		return nil, false
	}
	return func() {
		_ = windows.SetConsoleMode(outputHandle, outputMode)
	}, true
}
