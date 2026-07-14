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

func TestPermissionEditShowsFileAndLineRange(t *testing.T) {
	lines := permissionPayloadLines("Edit", map[string]any{
		"file_path":  "C:\\project\\auto.py",
		"old_string": "print(\"before\")",
		"new_string": "print(\"after\")",
		"start_line": 122,
		"end_line":   123,
	})
	got := strings.Join(lines, "\n")
	for _, want := range []string{"ACTION Edit", "FILE: project/auto.py", "LINES: 122-123", "CHANGE:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q in permission payload:\n%s", want, got)
		}
	}
}

func TestPermissionPromptExplainsSelector(t *testing.T) {
	got := buildPermissionPrompt(Style{}, permissionPayloadLines("Edit", map[string]any{
		"file_path": "auto.py",
	}))
	if strings.Contains(got, ";; stdin") {
		t.Fatalf("permission prompt should not expose the legacy ;; marker:\n%s", got)
	}
	if !strings.Contains(got, "Enter is not required") {
		t.Fatalf("permission prompt should explain single-key input:\n%s", got)
	}
}
