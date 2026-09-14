package main

import (
	"context"
	"sync"
)

// interrupter tracks the cancel function of the currently running agent
// operation so a Ctrl+C (SIGINT) can cancel just that operation without
// killing the whole REPL. The root context stays alive, so after the
// interrupted operation returns the user gets a fresh prompt.
type interrupter struct {
	mu      sync.Mutex
	current context.CancelFunc
}

// arm registers the cancel function for the in-flight operation.
func (i *interrupter) arm(cancel context.CancelFunc) {
	i.mu.Lock()
	i.current = cancel
	i.mu.Unlock()
}

// disarm clears the registered cancel function (call after the operation
// finishes or is cancelled).
func (i *interrupter) disarm() {
	i.mu.Lock()
	i.current = nil
	i.mu.Unlock()
}

// interrupt cancels the current operation if one is running. Returns true
// when an in-flight operation was cancelled.
func (i *interrupter) interrupt() bool {
	i.mu.Lock()
	c := i.current
	i.current = nil
	i.mu.Unlock()
	if c != nil {
		c()
		return true
	}
	return false
}

// globalInterrupter is the package-level instance shared between the signal
// handler and runAgentWithEmptyRetry.
var globalInterrupter = &interrupter{}
