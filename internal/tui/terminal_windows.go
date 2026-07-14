//go:build windows

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

func configureTerminalForBracketedPaste(in, out *os.File) (func(), bool) {
	inputHandle := windows.Handle(in.Fd())
	outputHandle := windows.Handle(out.Fd())

	var inputMode, outputMode uint32
	if windows.GetConsoleMode(inputHandle, &inputMode) != nil || windows.GetConsoleMode(outputHandle, &outputMode) != nil {
		return nil, false
	}
	if windows.SetConsoleMode(inputHandle, inputMode|windows.ENABLE_VIRTUAL_TERMINAL_INPUT) != nil {
		return nil, false
	}
	if windows.SetConsoleMode(outputHandle, outputMode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) != nil {
		_ = windows.SetConsoleMode(inputHandle, inputMode)
		return nil, false
	}
	return func() {
		_ = windows.SetConsoleMode(inputHandle, inputMode)
		_ = windows.SetConsoleMode(outputHandle, outputMode)
	}, true
}
