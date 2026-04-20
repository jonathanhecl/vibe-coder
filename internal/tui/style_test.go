package tui

import (
	"bytes"
	"strings"
	"testing"
)

func TestSplitThinkingExtractsBlock(t *testing.T) {
	t.Parallel()

	visible, thinking, leftover, hasMore := splitThinking("hi <think>plan</think> done")
	if !hasMore {
		t.Fatal("expected hasMore=true for completed think block")
	}
	if visible != "hi " {
		t.Fatalf("unexpected visible: %q", visible)
	}
	if thinking != "plan" {
		t.Fatalf("unexpected thinking: %q", thinking)
	}
	if leftover != " done" {
		t.Fatalf("unexpected leftover: %q", leftover)
	}
}

func TestSplitThinkingHandlesPartialOpen(t *testing.T) {
	t.Parallel()

	visible, thinking, leftover, hasMore := splitThinking("hello <thi")
	if hasMore {
		t.Fatal("expected hasMore=false for partial tag")
	}
	if visible != "hello " {
		t.Fatalf("unexpected visible: %q", visible)
	}
	if thinking != "" {
		t.Fatalf("expected no thinking yet, got %q", thinking)
	}
	if leftover != "<thi" {
		t.Fatalf("expected partial tag held back, got %q", leftover)
	}
}

func TestSplitThinkingHandlesPartialThinkingTag(t *testing.T) {
	t.Parallel()

	visible, thinking, leftover, hasMore := splitThinking("plan <thinking>step")
	if hasMore {
		t.Fatal("did not expect hasMore for unclosed thinking")
	}
	if visible != "plan " {
		t.Fatalf("unexpected visible: %q", visible)
	}
	if thinking != "step" {
		t.Fatalf("unexpected thinking buffer: %q", thinking)
	}
	if leftover != "" {
		t.Fatalf("unexpected leftover: %q", leftover)
	}
}

func TestFormatParamsStableOrder(t *testing.T) {
	t.Parallel()

	got := formatParams(map[string]any{"path": ".", "pattern": "*.go"})
	want := "(path=., pattern=*.go)"
	if got != want {
		t.Fatalf("formatParams = %q, want %q", got, want)
	}
}

func TestSummarizeOutputTruncatesAndCounts(t *testing.T) {
	t.Parallel()

	multi := strings.Repeat("line\n", 5)
	if got := summarizeOutput(multi); !strings.Contains(got, "5 lines") {
		t.Fatalf("expected line count, got %q", got)
	}
	long := strings.Repeat("a", 500)
	if got := summarizeOutput(long); !strings.HasSuffix(got, "…") {
		t.Fatalf("expected truncated suffix, got %q", got)
	}
}

func TestStyleDisabledWhenNotTTY(t *testing.T) {
	t.Parallel()

	s := NewStyle(&bytes.Buffer{})
	if s.Enabled() {
		t.Fatal("expected style disabled when writer is not a TTY")
	}
	if got := s.Bold("x"); got != "x" {
		t.Fatalf("expected raw text when disabled, got %q", got)
	}
}
