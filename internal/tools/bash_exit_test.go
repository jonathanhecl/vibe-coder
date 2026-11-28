package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// A command that printed output is evidence even when its last step exits
// non-zero: a diagnostic `sed; ls; cat` chain commonly ends on a missing file,
// and marking the whole call an error taught the model to distrust usable output.
func TestBashNonZeroExitWithOutputIsNotAnError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses sh")
	}
	res := NewBashTool().Execute(context.Background(), map[string]any{
		"command": "echo resultado; exit 3",
	})

	if res.IsError {
		t.Fatalf("non-zero exit with output must not be an error: %q", res.Output)
	}
	if !strings.Contains(res.Output, "resultado") {
		t.Fatalf("output missing: %q", res.Output)
	}
	if !strings.Contains(res.Output, "exit 3") {
		t.Fatalf("exit code missing from the output: %q", res.Output)
	}
	if res.HintsForModel == "" {
		t.Fatal("expected a hint explaining the exit status")
	}
}

// A command that failed without printing anything still needs to look like a
// failure, or a broken call would pass silently.
func TestBashSilentFailureStaysAnError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("shell fixture uses sh")
	}
	res := NewBashTool().Execute(context.Background(), map[string]any{"command": "exit 2"})

	if !res.IsError {
		t.Fatalf("silent failure must be an error: %q", res.Output)
	}
	if !strings.Contains(res.Output, "exit error") {
		t.Fatalf("expected the exit error note, got %q", res.Output)
	}
}
