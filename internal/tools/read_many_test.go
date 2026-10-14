package tools

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestReadManyReadsAllFiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	aPath := dir + "/a.txt"
	bPath := dir + "/b.txt"
	if err := os.WriteFile(aPath, []byte("alpha\nbeta\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bPath, []byte("gamma\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewReadManyTool().Execute(context.Background(), map[string]any{
		"paths": []any{aPath, bPath},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "===== 1/2: "+aPath) {
		t.Fatalf("missing first header: %s", res.Output)
	}
	if !strings.Contains(res.Output, "===== 2/2: "+bPath) {
		t.Fatalf("missing second header: %s", res.Output)
	}
	if !strings.Contains(res.Output, "alpha") || !strings.Contains(res.Output, "gamma") {
		t.Fatalf("missing file contents: %s", res.Output)
	}
}

func TestReadManyRespectsPerFileAlias(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	p := dir + "/one.txt"
	if err := os.WriteFile(p, []byte("line one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewReadManyTool().Execute(context.Background(), map[string]any{
		"file_paths": []any{p},
		"max_bytes":  1,
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "one.txt") {
		t.Fatalf("missing output: %s", res.Output)
	}
}
