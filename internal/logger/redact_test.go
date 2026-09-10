package logger

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRedactArgsMasksEnvValues(t *testing.T) {
	args := []string{"vibe-coder", "mcp", "add", "--env", "API_KEY=secret", "--env", "PORT=8080", "srv", "node"}
	got := RedactArgs(args)
	if got[4] != redactedValue {
		t.Errorf("expected --env value to be redacted, got %q", got[4])
	}
	if got[6] != redactedValue {
		t.Errorf("expected second --env value to be redacted, got %q", got[6])
	}
	joined := strings.Join(got, " ")
	if strings.Contains(joined, "secret") {
		t.Errorf("redacted args still contain secret: %q", joined)
	}
}

func TestRedactArgsMasksEnvEqualsForm(t *testing.T) {
	got := RedactArgs([]string{"mcp", "add", "--env=API_KEY=secret"})
	if !strings.Contains(got[2], redactedValue) || strings.Contains(got[2], "secret") {
		t.Errorf("expected --env= value to be redacted, got %q", got[2])
	}
}

func TestRedactArgsMasksSensitiveAssignments(t *testing.T) {
	got := RedactArgs([]string{"--token=abc123", "MODEL=llama", "GITHUB_TOKEN=xyz"})
	if strings.Contains(got[0], "abc123") {
		t.Errorf("expected token flag to be redacted, got %q", got[0])
	}
	if got[1] != "MODEL=llama" {
		t.Errorf("expected non-sensitive assignment untouched, got %q", got[1])
	}
	if strings.Contains(got[2], "xyz") {
		t.Errorf("expected sensitive assignment to be redacted, got %q", got[2])
	}
}

func TestRedactArgsMasksURLCredentials(t *testing.T) {
	got := RedactArgs([]string{"--ollama-host", "http://admin:s3cret@host:11434"})
	if strings.Contains(got[1], "s3cret") || strings.Contains(got[1], "admin") {
		t.Errorf("expected URL credentials to be redacted, got %q", got[1])
	}
	if !strings.Contains(got[1], "host:11434") {
		t.Errorf("expected host to be preserved, got %q", got[1])
	}
}

func TestRedactArgsDoesNotMutateInput(t *testing.T) {
	args := []string{"mcp", "add", "--env", "API_KEY=secret"}
	RedactArgs(args)
	if args[3] != "API_KEY=secret" {
		t.Errorf("input slice was mutated: %v", args)
	}
}

func TestLogFileIsOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	dir := t.TempDir()
	closer, err := Init(dir)
	if err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer closer.Close()
	info, err := os.Stat(filepath.Join(dir, "vibe-coder.log"))
	if err != nil {
		t.Fatalf("failed to stat log file: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("expected owner-only permissions on log file, got %04o", perm)
	}
}
