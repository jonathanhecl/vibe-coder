package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathMemoryResolvesAgainstCwd(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "AGENTS.md"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	mem := newPathMemory(tmp)

	abs, rescued, ok := mem.Resolve("AGENTS.md")
	if !ok {
		t.Fatalf("expected resolve via cwd to succeed")
	}
	if rescued {
		t.Fatalf("cwd resolve should not be marked rescued")
	}
	want := filepath.Join(tmp, "AGENTS.md")
	if abs != want {
		t.Fatalf("unexpected abs path: %q want %q", abs, want)
	}
}

func TestPathMemoryRescuesByBasename(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	nested := filepath.Join(tmp, "internal", "config")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir nested: %v", err)
	}
	target := filepath.Join(nested, "config.go")
	if err := os.WriteFile(target, []byte("package config"), 0o644); err != nil {
		t.Fatalf("seed nested: %v", err)
	}

	mem := newPathMemory(tmp)
	mem.RememberToolResult("Glob", map[string]any{}, target+"\n", false)

	abs, rescued, ok := mem.Resolve("config.go")
	if !ok {
		t.Fatalf("expected basename rescue to succeed")
	}
	if !rescued {
		t.Fatalf("basename resolve should be marked rescued")
	}
	if abs != target {
		t.Fatalf("unexpected abs path: %q want %q", abs, target)
	}
}

func TestPathMemoryDoesNotRescueAmbiguous(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	a := filepath.Join(tmp, "a", "config.go")
	b := filepath.Join(tmp, "b", "config.go")
	for _, p := range []string{a, b} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	mem := newPathMemory(tmp)
	mem.RememberToolResult("Glob", nil, a+"\n"+b, false)

	if _, _, ok := mem.Resolve("config.go"); ok {
		t.Fatalf("ambiguous basename should not be rescued")
	}
}

func TestPathMemoryRemembersFilePathParam(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	target := filepath.Join(tmp, "deep", "thing.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(target, []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mem := newPathMemory(tmp)
	mem.RememberToolResult("Read", map[string]any{"file_path": target}, "     1|x", false)

	abs, rescued, ok := mem.Resolve("thing.txt")
	if !ok || !rescued || abs != target {
		t.Fatalf("expected rescued resolve to %q got abs=%q rescued=%t ok=%t", target, abs, rescued, ok)
	}
}

func TestPathMemorySkipsErrors(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	bogus := filepath.Join(tmp, "missing.txt") // never created
	mem := newPathMemory(tmp)
	mem.RememberToolResult("Read", map[string]any{"file_path": bogus}, "boom", true)

	if _, _, ok := mem.Resolve("missing.txt"); ok {
		t.Fatalf("error result should not populate memory")
	}
}
