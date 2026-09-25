//go:build !windows

package tui

import (
	"io"
	"os"
	"testing"
	"time"
)

func TestStopESCMonitorExitsCleanly(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	u := &PlainUI{
		in:  r,
		out: io.Discard,
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	u.escStop = stop
	u.escDone = done

	restored := false
	u.escRestore = func() { restored = true }

	go u.escMonitorLoop(stop, done)

	time.Sleep(20 * time.Millisecond)

	stopped := make(chan struct{})
	go func() {
		u.StopESCMonitor()
		close(stopped)
	}()

	select {
	case <-stopped:
		// success
	case <-time.After(2 * time.Second):
		t.Fatal("StopESCMonitor deadlocked waiting for escMonitorLoop")
	}

	if !restored {
		t.Fatal("expected escRestore to be called")
	}
}

func TestStopESCMonitorNilSafe(t *testing.T) {
	u := &PlainUI{}
	// Calling StopESCMonitor when monitor was never started must not panic or block.
	u.StopESCMonitor()
}
