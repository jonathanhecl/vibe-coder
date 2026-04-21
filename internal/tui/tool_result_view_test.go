package tui

import (
	"strings"
	"testing"
)

func TestToolResultSummaryWriteShowsLines(t *testing.T) {
	t.Parallel()
	st := Style{}
	s := toolResultSummary(st, "Write", "Write successful.", false, map[string]any{
		"contents": "a\nb\nc",
	})
	if !strings.Contains(s, "+3") || !strings.Contains(s, "lines") {
		t.Fatalf("unexpected summary: %q", s)
	}
}

func TestToolResultSummaryEditShowsPlusMinus(t *testing.T) {
	t.Parallel()
	st := Style{}
	s := toolResultSummary(st, "Edit", "Write successful.", false, map[string]any{
		"old_string": "x",
		"new_string": "y\nz",
	})
	if !strings.Contains(s, "+2") || !strings.Contains(s, "−1") {
		t.Fatalf("unexpected summary: %q", s)
	}
}
