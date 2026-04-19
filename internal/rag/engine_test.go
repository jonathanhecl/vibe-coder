//go:build rag

package rag

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestIndexQueryAndFormatContext(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatalf("mkdir src: %v", err)
	}
	content := "package main\n\nfunc RunAgentLoop() {\n // loop\n}\n"
	if err := os.WriteFile(filepath.Join(src, "agent.go"), []byte(content), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	cfg := &config.Config{
		Cwd:      tmp,
		StateDir: tmp,
		RAGPath:  filepath.Join(tmp, "rag.db"),
	}
	e, err := NewEngine(cfg, nil)
	if err != nil {
		t.Fatalf("new engine: %v", err)
	}
	t.Cleanup(func() {
		_ = e.Close()
	})
	if err := e.IndexPath(context.Background(), src); err != nil {
		t.Fatalf("index path: %v", err)
	}
	chunks, err := e.Query(context.Background(), "where is agent loop", 3)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(chunks) == 0 {
		t.Fatalf("expected at least one chunk")
	}
	ctxText := e.FormatContext(chunks)
	if !strings.Contains(ctxText, "[RAG Context]") {
		t.Fatalf("expected formatted rag context, got %q", ctxText)
	}
}
