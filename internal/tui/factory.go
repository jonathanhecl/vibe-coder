package tui

import (
	"fmt"
	"strings"
)

// NewFromMode builds a terminal UI implementation from a user-facing mode.
// Supported values: plain, rich.
func NewFromMode(mode string) (UI, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "plain":
		return NewPlain(), nil
	case "rich":
		return NewRich(), nil
	default:
		return nil, fmt.Errorf("invalid ui mode %q: expected plain or rich", mode)
	}
}
