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

func TestProjectIsolatedRAGDatabasePath(t *testing.T) {
	stateDir := t.TempDir()
	cwd1 := filepath.Join(t.TempDir(), "project-alpha")
	cwd2 := filepath.Join(t.TempDir(), "project-beta")
	_ = os.MkdirAll(cwd1, 0o755)
	_ = os.MkdirAll(cwd2, 0o755)

	cfg1 := &config.Config{
		Cwd:      cwd1,
		StateDir: stateDir,
	}
	e1, err := NewEngine(cfg1, nil)
	if err != nil {
		t.Fatalf("new engine 1: %v", err)
	}
	defer e1.Close()

	cfg2 := &config.Config{
		Cwd:      cwd2,
		StateDir: stateDir,
	}
	e2, err := NewEngine(cfg2, nil)
	if err != nil {
		t.Fatalf("new engine 2: %v", err)
	}
	defer e2.Close()

	path1 := e1.Path()
	path2 := e2.Path()

	if path1 == "" || path2 == "" {
		t.Fatalf("expected non-empty db paths, got %q and %q", path1, path2)
	}
	if path1 == path2 {
		t.Fatalf("expected distinct project db paths for distinct cwds, got %q", path1)
	}

	key1, err1 := projectKey(cwd1)
	key2, err2 := projectKey(cwd2)
	if err1 != nil || err2 != nil {
		t.Fatalf("projectKey error: %v, %v", err1, err2)
	}

	expected1 := filepath.Join(stateDir, "rag", key1+".db")
	expected2 := filepath.Join(stateDir, "rag", key2+".db")

	if path1 != expected1 {
		t.Errorf("e1.Path() = %q, want %q", path1, expected1)
	}
	if path2 != expected2 {
		t.Errorf("e2.Path() = %q, want %q", path2, expected2)
	}
}
