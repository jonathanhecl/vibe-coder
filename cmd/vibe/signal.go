package main

import (
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// installSignalHandler ensures the terminal is always restored on exit, so a
// signal never leaves PowerShell or any TTY in raw mode (no echo, BackSpace
// rendered as ^H, etc.).
//
// Ctrl+C (SIGINT) does NOT exit the REPL: the first interrupt cancels only
// the in-flight agent operation (if any) and returns the user to a fresh
// prompt. Two Ctrl+C within 500 ms force a clean exit as a safety net for
// stuck state. SIGTERM always exits.
//
// Use Ctrl+D or the /exit (or bye) command to quit normally.
func installSignalHandler(ui interface{ Stop() }, sess interface{ Save() error }, intr *interrupter) {
	// SIGTERM: always exit cleanly.
	termCh := make(chan os.Signal, 2)
	signal.Notify(termCh, syscall.SIGTERM)
	go func() {
		<-termCh
		if ui != nil {
			ui.Stop()
		}
		if sess != nil {
			_ = sess.Save()
		}
		printByeOnInterrupt()
		os.Exit(130)
	}()

	// SIGINT (Ctrl+C): cancel in-flight work, stay in the REPL. A quick
	// double-tap forces exit so a stuck operation can always be escaped.
	intCh := make(chan os.Signal, 4)
	signal.Notify(intCh, os.Interrupt)
	go func() {
		var lastSignal time.Time
		for {
			<-intCh
			now := time.Now()
			if now.Sub(lastSignal) < 500*time.Millisecond {
				if ui != nil {
					ui.Stop()
				}
				os.Exit(130)
			}
			lastSignal = now
			if intr.interrupt() {
				fmt.Fprintln(os.Stdout, "\n^C (interrupted — type /exit or press Ctrl+D to quit)")
			} else {
				fmt.Fprintln(os.Stdout, "\n(Press Ctrl+D or type /exit to quit)")
			}
		}
	}()
}

// printByeOnInterrupt prints the goodbye line at most once. Both the signal
// handler and the read loop (stdin closed / interrupted) can run on exit.
var byeOnInterruptOnce sync.Once

func printByeOnInterrupt() {
	byeOnInterruptOnce.Do(func() {
		fmt.Fprintln(os.Stdout, "\nBye.")
	})
}
