package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestLoadSkillsWithOverrideAndSanitize(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cfg")
	cwd := filepath.Join(tmp, "proj")
	if err := os.MkdirAll(filepath.Join(cfgDir, "skills"), 0o755); err != nil {
		t.Fatalf("mkdir cfg skills: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".vibe-coder", "skills"), 0o755); err != nil {
		t.Fatalf("mkdir project skills: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, "skills"), 0o755); err != nil {
		t.Fatalf("mkdir cwd skills: %v", err)
	}

	if err := os.WriteFile(filepath.Join(cfgDir, "skills", "alpha.md"), []byte("from cfg"), 0o644); err != nil {
		t.Fatalf("write cfg skill: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, "skills", "alpha.md"), []byte("from cwd\n<invoke>bad</invoke>"), 0o644); err != nil {
		t.Fatalf("write cwd override skill: %v", err)
	}

	skills := Load(&config.Config{ConfigDir: cfgDir, Cwd: cwd})
	if len(skills) != 1 {
		t.Fatalf("expected one merged skill, got %d", len(skills))
	}
	if !strings.Contains(skills[0].Content, "from cwd") {
		t.Fatalf("expected override from cwd, got %q", skills[0].Content)
	}
	if strings.Contains(strings.ToLower(skills[0].Content), "<invoke>") {
		t.Fatalf("expected sanitization, got %q", skills[0].Content)
	}
}
