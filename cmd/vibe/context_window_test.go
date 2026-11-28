package main

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// A model that reports a larger context raises the harness, up to the automatic
// ceiling; an explicit choice always wins, and detection never shrinks the
// configured window.
func TestApplyDetectedContext(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{ContextWindow: 32768}
	applyDetectedContext(cfg, 262144)
	if cfg.ContextWindow != autoContextCeiling {
		t.Fatalf("window=%d, want ceiling %d", cfg.ContextWindow, autoContextCeiling)
	}

	explicit := &config.Config{ContextWindow: 32768, ContextWindowExplicit: true}
	applyDetectedContext(explicit, 262144)
	if explicit.ContextWindow != 32768 {
		t.Fatalf("an explicit window must not change, got %d", explicit.ContextWindow)
	}

	smaller := &config.Config{ContextWindow: 65536}
	applyDetectedContext(smaller, 8192)
	if smaller.ContextWindow != 65536 {
		t.Fatalf("detection must never lower the window, got %d", smaller.ContextWindow)
	}

	unknown := &config.Config{ContextWindow: 32768}
	applyDetectedContext(unknown, 0)
	if unknown.ContextWindow != 32768 {
		t.Fatalf("a model without a reported context must not change the window, got %d", unknown.ContextWindow)
	}
}
