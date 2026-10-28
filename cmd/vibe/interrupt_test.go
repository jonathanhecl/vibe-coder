package main

import (
	"context"
	"testing"
	"time"
)

func TestInterrupterInterruptsArmedOperation(t *testing.T) {
	intr := &interrupter{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intr.arm(cancel)
	defer intr.disarm()

	if !intr.interrupt() {
		t.Fatal("expected interrupt() to return true when an operation is armed")
	}
	select {
	case <-ctx.Done():
	case <-time.After(100 * time.Millisecond):
		t.Fatal("armed context was not cancelled by interrupt()")
	}
}

func TestInterrupterReturnsFalseWhenDisarmed(t *testing.T) {
	intr := &interrupter{}
	if intr.interrupt() {
		t.Fatal("expected interrupt() to return false when no operation is armed")
	}
}

// A second Ctrl+C while the turn is still unwinding must keep reporting the
// cancellation instead of telling the user to type /exit: the agent, not the
// prompt, still owns the terminal.
func TestInterrupterStaysArmedUntilDisarm(t *testing.T) {
	intr := &interrupter{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intr.arm(cancel)
	defer intr.disarm()

	if !intr.interrupt() {
		t.Fatal("first interrupt must cancel the armed operation")
	}
	if !intr.interrupt() {
		t.Fatal("a second interrupt during unwinding must still cancel the same turn")
	}
	if ctx.Err() == nil {
		t.Fatal("armed context must be cancelled")
	}
}

func TestInterrupterDisarmClearsCurrent(t *testing.T) {
	intr := &interrupter{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intr.arm(cancel)
	intr.disarm()

	if intr.interrupt() {
		t.Fatal("expected interrupt() to return false after disarm()")
	}
	if ctx.Err() != nil {
		t.Fatal("context should not be cancelled after disarm without interrupt")
	}
}
