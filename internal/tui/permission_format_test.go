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

// TestPermissionPayloadShowsGravity proves the command weight reaches the card
// and that the bookkeeping keys stay out of the raw payload dump.
func TestPermissionPayloadShowsGravity(t *testing.T) {
	t.Parallel()
	lines := permissionPayloadLines("Bash", map[string]any{
		"command":         "cat a.txt > existing.txt",
		"gravity":         "DESTRUCTIVE",
		"gravity_warning": "deletes or overwrites data that already exists",
	})
	joined := strings.Join(lines, "\n")

	for _, want := range []string{
		"GRAVITY DESTRUCTIVE",
		"WHY: deletes or overwrites data that already exists",
		"cat a.txt > existing.txt",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("expected the card to contain %q, got:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "gravity:") || strings.Contains(joined, "gravity_warning:") {
		t.Errorf("gravity bookkeeping keys must not appear as payload entries, got:\n%s", joined)
	}
}

// TestPermissionPayloadOmitsGravityWarningForPlainWrites proves an ordinary
// write is labelled without pretending it is dangerous.
func TestPermissionPayloadOmitsGravityWarningForPlainWrites(t *testing.T) {
	t.Parallel()
	lines := permissionPayloadLines("Bash", map[string]any{
		"command": "mkdir -p internal",
		"gravity": "WRITE",
	})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "GRAVITY WRITE") {
		t.Errorf("expected the card to contain the WRITE label, got:\n%s", joined)
	}
	if strings.Contains(joined, "WHY:") {
		t.Errorf("a plain write must carry no reason line, got:\n%s", joined)
	}
}

func TestPermissionPayloadShowsFullPathOnDomainWarning(t *testing.T) {
	t.Parallel()
	lines := permissionPayloadLines("Write", map[string]any{
		"file_path":      "/root/exports/newdata.md",
		"domain_warning": "path is outside workspace domain (/media/datamac/temp)",
		"contents":       "test content",
	})
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "FILE: /root/exports/newdata.md") {
		t.Errorf("expected card to show full uncompacted path when domain warning is present, got:\n%s", joined)
	}
	if strings.Contains(joined, "FILE: exports/newdata.md") {
		t.Errorf("card should not deceive user with compacted path on domain warning, got:\n%s", joined)
	}
}
