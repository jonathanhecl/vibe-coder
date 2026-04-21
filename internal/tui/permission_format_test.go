package tui

import (
	"strings"
	"testing"
)

func TestWrapParagraph(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("word ", 30)
	lines := wrapParagraph(long, 20)
	if len(lines) < 2 {
		t.Fatalf("expected multiple lines, got %d", len(lines))
	}
	for _, ln := range lines {
		if utfCount(ln) > 20 {
			t.Fatalf("line too long: %d %q", utfCount(ln), ln)
		}
	}
}

func utfCount(s string) int {
	n := 0
	for range s {
		n++
	}
	return n
}

func TestPermissionPayloadLinesWraps(t *testing.T) {
	t.Parallel()
	p := map[string]any{
		"file_path": "README.md",
		"contents":  strings.Repeat("Lorem ipsum dolor ", 40),
	}
	lines := permissionPayloadLines("Write", p)
	if len(lines) < 4 {
		t.Fatalf("expected several lines, got %d: %v", len(lines), lines)
	}
}

func TestFitGateLine(t *testing.T) {
	t.Parallel()
	s := strings.Repeat("あ", 80)
	out := fitGateLine(s, 10)
	if utfCount(out) > 10 {
		t.Fatalf("got len %d", utfCount(out))
	}
}
