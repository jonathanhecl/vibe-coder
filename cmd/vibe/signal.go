package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// installSignalHandler ensures the terminal is always restored on exit, so a
// Ctrl+C never leaves PowerShell or any TTY in raw mode (no echo, BackSpace
// rendered as ^H, etc.). The first signal cancels in-flight work, restores the
// terminal, persists the session, and then forces a clean exit shortly after
// to avoid blocked stdin reads after cancellation.
func installSignalHandler(ui interface{ Stop() }, sess interface{ Save() error }, cancel context.CancelFunc) {
	sigCh := make(chan os.Signal, 4)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		if ui != nil {
			ui.Stop()
		}
		if cancel != nil {
			cancel()
		}
		if sess != nil {
			_ = sess.Save()
		}
		printByeOnInterrupt()

		go func() {
			<-sigCh
			if ui != nil {
				ui.Stop()
			}
			os.Exit(130)
		}()

		time.Sleep(400 * time.Millisecond)
		if ui != nil {
			ui.Stop()
		}
		os.Exit(130)
	}()
}

// printByeOnInterrupt prints the goodbye line at most once. Both the signal
// handler and the read loop (stdin closed / interrupted) can run on Ctrl+C.
var byeOnInterruptOnce sync.Once

func printByeOnInterrupt() {
	byeOnInterruptOnce.Do(func() {
		fmt.Fprintln(os.Stdout, "\nBye.")
	})
}
