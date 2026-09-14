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
