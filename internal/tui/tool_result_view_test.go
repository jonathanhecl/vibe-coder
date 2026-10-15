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

func TestShowToolResultPrintsFullBashCommandOnError(t *testing.T) {
	long := "cd /d C:/Users/gense/Desktop/dev/repos/superlong-project && ffprobe output.mp4"
	if !bashCommandTruncated(long) {
		t.Fatalf("test command should exceed the header limit: %q", long)
	}
	if bashCommandTruncated("ls") {
		t.Fatal("short command must not count as truncated")
	}
	var b strings.Builder
	u := &PlainUI{out: &b, style: Style{}}
	u.ShowToolCall("Bash", map[string]any{"command": long})
	u.ShowToolResult("Bash", "El sistema no puede encontrar la ruta especificada.", true, map[string]any{"command": long})
	if out := b.String(); !strings.Contains(out, long) {
		t.Fatalf("expected full command in output, got %q", out)
	}
}

func TestShowToolResultOmitsFullBashCommandWhenShort(t *testing.T) {
	var b strings.Builder
	u := &PlainUI{out: &b, style: Style{}}
	u.ShowToolCall("Bash", map[string]any{"command": "ls"})
	u.ShowToolResult("Bash", "boom", true, map[string]any{"command": "ls"})
	for _, line := range strings.Split(b.String(), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "$ ") {
			t.Fatalf("short command must not be duplicated, got %q", b.String())
		}
	}
}

func TestShowToolResultBackgroundStillRunning(t *testing.T) {
	var b strings.Builder
	u := &PlainUI{out: &b, style: Style{}}
	output := "[task_id: bg-1]\n[status: STILL RUNNING - execution in background]\n[pid: 1234]\n[elapsed: 500ms]\n"
	u.ShowToolCall("BackgroundRun", map[string]any{"command": "sleep 10"})
	u.ShowToolResult("BackgroundRun", output, false, map[string]any{"command": "sleep 10"})

	res := b.String()
	if !strings.Contains(res, "running in background (not finished)") {
		t.Fatalf("expected running in background tag, got: %s", res)
	}
	if !strings.Contains(res, "bg-1: still executing in background") {
		t.Fatalf("expected task id in summary, got: %s", res)
	}
	if strings.Contains(res, "done") {
		t.Fatalf("must not say 'done' for running task, got: %s", res)
	}
}
