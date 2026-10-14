package skills

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestDetectCandidates(t *testing.T) {
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")

	// OpenCode dir with two skills; Devin with one; empty Claude and Codex
	// dirs; missing Agents dir.
	opencode := filepath.Join(home, ".config", "opencode", "skills")
	writeSkillMD(t, opencode, "one", "", "body")
	writeSkillMD(t, opencode, "two", "", "body")
	devin := filepath.Join(home, ".config", "devin", "skills")
	writeSkillMD(t, devin, "three", "", "body")
	for _, empty := range []string{".claude/skills", ".codex/skills"} {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(empty)), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	got := DetectCandidates(home)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates, got %+v", got)
	}
	if got[0].Tool != "OpenCode" || got[0].Count != 2 || got[0].Dir != opencode {
		t.Fatalf("unexpected first candidate: %+v", got[0])
	}
	if got[1].Tool != "Devin" || got[1].Count != 1 {
		t.Fatalf("unexpected second candidate: %+v", got[1])
	}
	if DetectCandidates("") != nil {
		t.Fatal("empty home should yield no candidates")
	}
}

func TestCountSkills(t *testing.T) {
	dir := t.TempDir()
	if CountSkills(dir) != 0 {
		t.Fatal("expected 0 for an empty dir")
	}
	if CountSkills(filepath.Join(dir, "missing")) != 0 {
		t.Fatal("expected 0 for a missing dir")
	}
	writeSkillMD(t, dir, "a", "", "body")
	if err := os.WriteFile(filepath.Join(dir, "b.md"), []byte("flat"), 0o644); err != nil {
		t.Fatal(err)
	}
	if CountSkills(dir) != 2 {
		t.Fatalf("expected 2 skills, got %d", CountSkills(dir))
	}
}

func TestReadOnlyDirsResolvesSymlinks(t *testing.T) {
	tmp := t.TempDir()
	real := t.TempDir()
	writeSkillMD(t, real, "s", "", "body")
	root := filepath.Join(tmp, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(real, "s"), filepath.Join(root, "s")); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Cwd: t.TempDir(), ConfigDir: t.TempDir(), SkillsDirs: []string{root}}
	dirs := ReadOnlyDirs(cfg)
	found := false
	for _, d := range dirs {
		if d == filepath.Join(real, "s") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the resolved skill dir %q in %v", filepath.Join(real, "s"), dirs)
	}
}
