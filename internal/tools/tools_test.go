package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
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
	for _, name := range []string{
		"Read", "Write", "Edit", "Glob", "Bash",
		"Grep", "WebFetch", "WebSearch", "NotebookEdit",
		"TaskCreate", "TaskList", "TaskGet", "TaskUpdate", "AskUserQuestion",
	} {
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

func TestReadLongSingleLine(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "huge-line.txt")
	// Default bufio.Scanner fails around 64KB; Godot .tscn lines can be far larger.
	longLine := strings.Repeat("x", 100*1024)
	if err := os.WriteFile(p, []byte(longLine+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewReadTool().Execute(context.Background(), map[string]any{"file_path": p})
	if res.IsError {
		t.Fatalf("read long line: %s", res.Output)
	}
	if !strings.HasPrefix(res.Output, "     1|") || !strings.Contains(res.Output, strings.Repeat("x", 1024)) {
		t.Fatalf("expected numbered long line in output: %s", truncate(res.Output, 200))
	}
}

func TestReadPartialRange(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	p := filepath.Join(tmp, "range.txt")
	if err := os.WriteFile(p, []byte("one\ntwo\nthree\nfour\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewReadTool().Execute(context.Background(), map[string]any{
		"file_path":  p,
		"start_line": 2,
		"limit":      2,
	})
	if res.IsError {
		t.Fatalf("partial read failed: %s", res.Output)
	}
	if strings.Contains(res.Output, "one") || strings.Contains(res.Output, "four") {
		t.Fatalf("partial read included out-of-range lines: %s", res.Output)
	}
	if !strings.Contains(res.Output, "     2|two") || !strings.Contains(res.Output, "     3|three") {
		t.Fatalf("partial read missed expected lines: %s", res.Output)
	}
	if !strings.Contains(res.Output, "read truncated") {
		t.Fatalf("expected truncation hint, got: %s", res.Output)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func TestGrepModes(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	file := filepath.Join(tmp, "a.txt")
	if err := os.WriteFile(file, []byte("hello\nvibe\nhello"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewGrepTool()
	res := tool.Execute(context.Background(), map[string]any{
		"pattern": "hello",
		"path":    tmp,
	})
	if res.IsError || !strings.Contains(res.Output, "hello") {
		t.Fatalf("unexpected grep content output: %s", res.Output)
	}
	res = tool.Execute(context.Background(), map[string]any{
		"pattern":     "hello",
		"path":        tmp,
		"output_mode": "count",
	})
	if !strings.Contains(res.Output, "a.txt:2") {
		t.Fatalf("unexpected grep count output: %s", res.Output)
	}
}

func TestGrepSkipsIgnoredDirsAndLimitsOutput(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "a.txt"), []byte("hit\nhit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(tmp, "node_modules")
	if err := os.MkdirAll(ignored, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ignored, "b.txt"), []byte("hit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := NewGrepTool().Execute(context.Background(), map[string]any{
		"pattern":    "hit",
		"path":       tmp,
		"head_limit": 1,
	})
	if res.IsError {
		t.Fatalf("grep failed: %s", res.Output)
	}
	if strings.Contains(res.Output, "node_modules") {
		t.Fatalf("grep should skip ignored dirs: %s", res.Output)
	}
	if got := len(strings.Split(strings.TrimSpace(res.Output), "\n")); got != 1 {
		t.Fatalf("expected one output line, got %d: %s", got, res.Output)
	}
}

func TestWebToolsBlockLocalhost(t *testing.T) {
	t.Parallel()
	fetch := NewWebFetchTool().Execute(context.Background(), map[string]any{"url": "http://localhost:8080"})
	if !fetch.IsError {
		t.Fatalf("expected localhost fetch to be blocked")
	}
	search := NewWebSearchTool().Execute(context.Background(), map[string]any{"query": "golang"})
	if search.IsError && !strings.Contains(search.Output, "failed") {
		t.Fatalf("unexpected websearch error: %s", search.Output)
	}
}

func TestNotebookEditAndTaskTools(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	nbPath := filepath.Join(tmp, "n.ipynb")
	nb := map[string]any{
		"cells": []map[string]any{
			{"cell_type": "code", "source": []string{"print('a')"}},
		},
	}
	raw, _ := json.Marshal(nb)
	if err := os.WriteFile(nbPath, raw, 0o644); err != nil {
		t.Fatalf("write notebook: %v", err)
	}

	edit := NewNotebookEditTool().Execute(context.Background(), map[string]any{
		"notebook_path": nbPath,
		"cell_index":    0,
		"old_string":    "a",
		"new_string":    "b",
		"is_new_cell":   false,
	})
	if edit.IsError {
		t.Fatalf("notebook edit failed: %s", edit.Output)
	}

	create := NewTaskCreateTool().Execute(context.Background(), map[string]any{"content": "ship mvp"})
	if create.IsError {
		t.Fatalf("task create failed: %s", create.Output)
	}
	list := NewTaskListTool().Execute(context.Background(), map[string]any{})
	if list.IsError || !strings.Contains(list.Output, "ship mvp") {
		t.Fatalf("task list failed: %s", list.Output)
	}
}

type fakeChatClient struct{}

func (fakeChatClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (fakeChatClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (fakeChatClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{Content: "summary"}, nil
}
func (fakeChatClient) Pull(context.Context, string, func(ollama.PullEvent)) error { return nil }
func (fakeChatClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: "subagent-ok", Done: true}
	close(ch)
	return ch, nil
}

func TestSubAgentAndParallelAgents(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Model: "llama3.2:3b", ContextWindow: 2048, MaxTokens: 128, Temperature: 0.2}
	sub := NewSubAgentTool(cfg, fakeChatClient{})
	out := sub.Execute(context.Background(), map[string]any{"prompt": "hello"})
	if out.IsError || !strings.Contains(out.Output, "subagent-ok") {
		t.Fatalf("subagent failed: %s", out.Output)
	}
	par := NewParallelAgentsTool(sub)
	res := par.Execute(context.Background(), map[string]any{
		"tasks": []any{
			map[string]any{"prompt": "one"},
			map[string]any{"prompt": "two"},
		},
	})
	if res.IsError || !strings.Contains(res.Output, "subagent-ok") {
		t.Fatalf("parallel agents failed: %s", res.Output)
	}
}
