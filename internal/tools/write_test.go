package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAppendPreservesExistingContent(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "hosts")
	if err := os.WriteFile(path, []byte("127.0.0.1 localhost\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewWriteTool().Execute(context.Background(), map[string]any{
		"file_path": path,
		"contents":  "192.168.0.33 mac-mini.local\n",
		"append":    true,
	})
	if res.IsError {
		t.Fatalf("append failed: %s", res.Output)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "127.0.0.1 localhost\n192.168.0.33 mac-mini.local\n"
	if string(data) != want {
		t.Fatalf("append content = %q, want %q", data, want)
	}
}

func TestWriteAppendCreatesMissingFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "new.jsonl")
	res := NewWriteTool().Execute(context.Background(), map[string]any{
		"file_path": path,
		"contents":  "{\"a\":1}\n",
		"append":    true,
	})
	if res.IsError {
		t.Fatalf("append to new file failed: %s", res.Output)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "{\"a\":1}\n" {
		t.Fatalf("new appended file = %q err=%v", data, err)
	}
}

func TestWriteOverwriteStillReplaces(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	path := filepath.Join(tmp, "f.txt")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewWriteTool().Execute(context.Background(), map[string]any{
		"file_path": path,
		"contents":  "new\n",
	})
	if res.IsError {
		t.Fatalf("overwrite failed: %s", res.Output)
	}
	if data, _ := os.ReadFile(path); string(data) != "new\n" {
		t.Fatalf("overwrite content = %q, want %q", data, "new\n")
	}
}
