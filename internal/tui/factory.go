package tui

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// NewFromMode builds a terminal UI implementation from a user-facing mode.
// Supported values: plain, cli, tui, hybrid, rich.
func NewFromMode(cfg *config.Config) (UI, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.UI)) {
	case "", "plain", "cli":
		return NewPlain(cfg), nil
	case "tui", "hybrid":
		return NewHybrid(cfg), nil
	case "rich":
		return NewRich(cfg), nil
	default:
		return nil, fmt.Errorf("invalid ui mode %q: expected plain, cli, tui, or rich", cfg.UI)
	}
}
