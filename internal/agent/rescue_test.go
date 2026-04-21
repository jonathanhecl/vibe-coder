package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPathMemoryCandidatesReturnsSortedAbsPaths(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	dirs := []string{"b", "a", "c"}
	var want []string
	for _, d := range dirs {
		full := filepath.Join(tmp, d)
		if err := os.MkdirAll(full, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		p := filepath.Join(full, "config.go")
		if err := os.WriteFile(p, []byte("package x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
		want = append(want, p)
	}
	// Expected sorted lexicographically by absolute path.
	sortStrings(want)

	mem := newPathMemory(tmp)
	for _, w := range want {
		mem.add(w)
	}
	got := mem.Candidates("config.go")
	if len(got) != len(want) {
		t.Fatalf("expected %d candidates, got %d (%v)", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("candidate %d = %q, want %q", i, got[i], want[i])
		}
	}
	if c := mem.Candidates("nope.go"); c != nil {
		t.Fatalf("expected nil for unknown basename, got %v", c)
	}
}

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

func TestPathMemoryResolvesGodotResURI(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	mainDir := filepath.Join(tmp, "Main")
	if err := os.MkdirAll(mainDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	target := filepath.Join(mainDir, "MainMapContainer.gd")
	if err := os.WriteFile(target, []byte("extends Node"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	mem := newPathMemory(tmp)

	// Do not use filepath.Join for the second case: on Windows, Join turns
	// "res://..." into "res:\..." and breaks the res:// marker (same bug users hit).
	brokenConcat := tmp + string(filepath.Separator) + `res://Main/MainMapContainer.gd`
	cases := []string{
		`res://Main/MainMapContainer.gd`,
		brokenConcat,
	}
	for _, c := range cases {
		abs, rescued, ok := mem.Resolve(c)
		if !ok || !rescued {
			t.Fatalf("Resolve(%q): ok=%v rescued=%v", c, ok, rescued)
		}
		if abs != target {
			t.Fatalf("Resolve(%q) = %q, want %q", c, abs, target)
		}
	}
}
