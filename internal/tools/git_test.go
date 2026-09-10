package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	gitx "github.com/jonathanhecl/vibe-coder/internal/git"
)

func gitAvailable() bool {
	_, err := exec.LookPath("git")
	return err == nil
}

func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	run("add", "a.txt")
	run("commit", "-m", "init")
}

func TestGitStatusAndDiff(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	initGitRepo(t, dir)
	t.Chdir(dir)

	if out := NewGitStatusTool().Execute(context.Background(), map[string]any{}); out.IsError || !strings.Contains(out.Output, "clean") {
		t.Fatalf("expected clean status, got %+v", out)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello changed\n"), 0o644); err != nil {
		t.Fatalf("modify fixture: %v", err)
	}
	status := NewGitStatusTool().Execute(context.Background(), map[string]any{})
	if status.IsError || !strings.Contains(status.Output, "M") {
		t.Fatalf("expected dirty status, got %+v", status)
	}
	diff := NewGitDiffTool().Execute(context.Background(), map[string]any{"file_path": "a.txt"})
	if diff.IsError || !strings.Contains(diff.Output, "hello changed") {
		t.Fatalf("expected diff content, got %+v", diff)
	}
}

func TestGitUndoWithoutCheckpoint(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	initGitRepo(t, dir)
	t.Chdir(dir)

	out := NewGitUndoTool().Execute(context.Background(), map[string]any{})
	if out.IsError {
		t.Fatalf("expected info result, got %+v", out)
	}
	if !strings.Contains(out.Output, "No vibe-coder checkpoint") {
		t.Fatalf("expected no-checkpoint note, got %q", out.Output)
	}
}

func TestGitUndoRestoresCheckpoint(t *testing.T) {
	if !gitAvailable() {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	initGitRepo(t, dir)
	target := filepath.Join(dir, "a.txt")
	cp := gitx.NewCheckpoint(dir)
	if err := cp.Create("test", target); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("bad change\n"), 0o644); err != nil {
		t.Fatalf("modify fixture: %v", err)
	}
	if err := cp.Complete(); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	out := NewGitUndoTool().Execute(context.Background(), map[string]any{})
	if out.IsError {
		t.Fatalf("undo failed: %+v", out)
	}
	if !strings.Contains(out.Output, "Restored") {
		t.Fatalf("expected restore note, got %q", out.Output)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read restored file: %v", err)
	}
	if string(data) != "hello\n" {
		t.Fatalf("expected pre-edit contents restored, got %q", data)
	}
}
