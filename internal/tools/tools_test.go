package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteReadEditGlob(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	target := filepath.Join(tmp, "notes.txt")

	writeRes := NewWriteTool().Execute(context.Background(), map[string]any{
		"file_path": target,
		"contents":  "hello\nworld",
	})
	if writeRes.IsError {
		t.Fatalf("write failed: %s", writeRes.Output)
	}

	readRes := NewReadTool().Execute(context.Background(), map[string]any{
		"file_path": target,
	})
	if readRes.IsError || !strings.Contains(readRes.Output, "hello") {
		t.Fatalf("read failed: %s", readRes.Output)
	}

	editRes := NewEditTool().Execute(context.Background(), map[string]any{
		"file_path":  target,
		"old_string": "world",
		"new_string": "vibe",
	})
	if editRes.IsError {
		t.Fatalf("edit failed: %s", editRes.Output)
	}

	globRes := NewGlobTool().Execute(context.Background(), map[string]any{
		"pattern": "*.txt",
		"path":    tmp,
	})
	if globRes.IsError || !strings.Contains(globRes.Output, "notes.txt") {
		t.Fatalf("glob failed: %s", globRes.Output)
	}
}

func TestBashTool(t *testing.T) {
	t.Parallel()
	res := NewBashTool().Execute(context.Background(), map[string]any{
		"command": "echo hello",
	})
	if res.IsError {
		t.Fatalf("bash failed: %s", res.Output)
	}
	if !strings.Contains(strings.ToLower(res.Output), "hello") {
		t.Fatalf("unexpected bash output: %s", res.Output)
	}
}

func TestRegistryDefaults(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	reg.RegisterDefaults()
	for _, name := range []string{"Read", "Write", "Edit", "Glob", "Bash"} {
		if reg.Get(name) == nil {
			t.Fatalf("missing default tool: %s", name)
		}
	}
}

func TestReadRejectsProtectedPath(t *testing.T) {
	t.Parallel()
	// This should be blocked on unix. On windows the path won't exist but still safe to assert error.
	path := "/proc/self/environ"
	if os.PathSeparator == '\\' {
		path = `C:\Windows\System32\config\SAM`
	}
	res := NewReadTool().Execute(context.Background(), map[string]any{"file_path": path})
	if !res.IsError {
		t.Fatalf("expected protected path error")
	}
}
