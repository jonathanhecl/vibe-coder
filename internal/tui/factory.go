package tui

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// NewFromMode builds a terminal UI implementation from a user-facing mode.
// Supported values: plain, rich.
func NewFromMode(cfg *config.Config) (UI, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.UI)) {
	case "", "plain":
		return NewPlain(cfg), nil
	case "rich":
		return NewRich(cfg), nil
	default:
		return nil, fmt.Errorf("invalid ui mode %q: expected plain or rich", cfg.UI)
	}
}
