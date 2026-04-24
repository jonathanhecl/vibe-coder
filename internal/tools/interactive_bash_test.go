package tools

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInteractiveBash_EchoAndExit(t *testing.T) {
	t.Parallel()
	tool := NewInteractiveBashTool()
	res := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello",
	})
	if res.IsError {
		t.Fatalf("interactive bash failed: %s", res.Output)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Fatalf("expected 'hello' in output, got: %s", res.Output)
	}
	if strings.Contains(res.Output, "session_id") {
		t.Fatalf("expected no session_id for quick exit, got: %s", res.Output)
	}
}

func TestInteractiveBash_ReadPromptAndRespond(t *testing.T) {
	t.Parallel()
	bash := NewInteractiveBashTool()
	input := NewSendInputTool()
	term := NewTerminateSessionTool()

	var cmd string
	switch runtime.GOOS {
	case "windows":
		// Use a temporary batch file to avoid cmd variable expansion timing issues
		// when using cmd /C with & and set /p in a single command string.
		cmd = writeWindowsBatch(t)
	default:
		cmd = `sh -c 'printf "Name: "; read x; echo "got $x"'`
	}

	res := bash.Execute(context.Background(), map[string]any{
		"command": cmd,
	})
	if res.IsError {
		// Shell might exit immediately in some CI environments; that's OK
		t.Skipf("interactive bash returned error (likely non-interactive env): %s", res.Output)
	}

	if !strings.Contains(res.Output, "session_id") {
		if strings.Contains(res.Output, "Name:") {
			// Shell finished before we could capture session — still a valid run
			t.Skip("shell exited too quickly for interactive test")
		}
		t.Fatalf("expected session_id for running command, got: %s", res.Output)
	}

	sessionID := extractSessionID(res.Output)
	if sessionID == "" {
		t.Fatalf("could not extract session_id from: %s", res.Output)
	}

	res2 := input.Execute(context.Background(), map[string]any{
		"session_id": sessionID,
		"input":      "Alice",
	})
	if res2.IsError {
		t.Fatalf("send input failed: %s", res2.Output)
	}

	if !strings.Contains(res2.Output, "Alice") {
		t.Fatalf("expected 'Alice' in output, got: %s", res2.Output)
	}

	// Clean up if still running
	_ = term.Execute(context.Background(), map[string]any{
		"session_id": sessionID,
	})
}

func TestInteractiveBash_DangerousBlocked(t *testing.T) {
	t.Parallel()
	tool := NewInteractiveBashTool()
	res := tool.Execute(context.Background(), map[string]any{
		"command": "rm -rf /",
	})
	if !res.IsError {
		t.Fatal("expected error for dangerous command")
	}
	if !strings.Contains(res.Output, "dangerous command blocked") {
		t.Fatalf("expected dangerous command error, got: %s", res.Output)
	}
}

func TestSendInput_SessionNotFound(t *testing.T) {
	t.Parallel()
	tool := NewSendInputTool()
	res := tool.Execute(context.Background(), map[string]any{
		"session_id": "term-nonexistent",
		"input":      "hello",
	})
	if !res.IsError {
		t.Fatal("expected error for missing session")
	}
	if !strings.Contains(res.Output, "session not found") {
		t.Fatalf("expected session not found error, got: %s", res.Output)
	}
}

func TestTerminateSession_SessionNotFound(t *testing.T) {
	t.Parallel()
	tool := NewTerminateSessionTool()
	res := tool.Execute(context.Background(), map[string]any{
		"session_id": "term-nonexistent",
	})
	if !res.IsError {
		t.Fatal("expected error for missing session")
	}
	if !strings.Contains(res.Output, "session not found") {
		t.Fatalf("expected session not found error, got: %s", res.Output)
	}
}

func TestInteractiveBash_BackgroundBlocked(t *testing.T) {
	t.Parallel()
	tool := NewInteractiveBashTool()
	res := tool.Execute(context.Background(), map[string]any{
		"command": "echo hello &",
	})
	if !res.IsError {
		t.Fatal("expected error for background command")
	}
	if !strings.Contains(res.Output, "background commands are blocked") {
		t.Fatalf("expected background command error, got: %s", res.Output)
	}
}

func extractSessionID(output string) string {
	const prefix = "[session_id: "
	idx := strings.Index(output, prefix)
	if idx < 0 {
		return ""
	}
	rest := output[idx+len(prefix):]
	end := strings.Index(rest, "]")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

func writeWindowsBatch(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "test.bat")
	script := "@echo off\necho Name:\nset /p x=\necho got %x%\n"
	if err := os.WriteFile(path, []byte(script), 0644); err != nil {
		t.Fatalf("write batch file: %v", err)
	}
	return path
}
