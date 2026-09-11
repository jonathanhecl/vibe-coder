package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadReturnsEveryLine(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "two.jsonl")
	if err := os.WriteFile(p, []byte("{\"id\":\"a\"}\n{\"id\":\"b\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	full := NewReadTool().Execute(context.Background(), map[string]any{"file_path": p})
	if full.IsError {
		t.Fatalf("read failed: %s", full.Output)
	}
	if !strings.Contains(full.Output, `"a"`) || !strings.Contains(full.Output, `"b"`) {
		t.Fatalf("default read dropped a line: %q", full.Output)
	}

	limited := NewReadTool().Execute(context.Background(), map[string]any{"file_path": p, "limit": 100})
	if limited.IsError {
		t.Fatalf("limited read failed: %s", limited.Output)
	}
	if !strings.Contains(limited.Output, `"a"`) || !strings.Contains(limited.Output, `"b"`) {
		t.Fatalf("limit=100 dropped a line: %q", limited.Output)
	}
}
