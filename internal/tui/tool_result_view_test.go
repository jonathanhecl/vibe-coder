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

func TestPrintEditDiffPreviewWithDiff(t *testing.T) {
	var b strings.Builder
	st := Style{}
	printEditDiffPreview(&b, st, map[string]any{"_diff": "@@ -1,3 +1,3 @@\n a\n-b\n+c\n d"})
	out := b.String()
	if !strings.Contains(out, "@@") || !strings.Contains(out, "-") || !strings.Contains(out, "+") {
		t.Fatalf("expected colored diff output, got %q", out)
	}
}

func TestPrintColoredDiffTruncates(t *testing.T) {
	var b strings.Builder
	st := Style{}
	big := strings.Repeat("+x\n", 60)
	printColoredDiff(&b, st, big)
	if !strings.Contains(b.String(), "truncated") {
		t.Fatal("expected truncation indicator")
	}
}
