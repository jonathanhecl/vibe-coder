package permissions

import (
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestReadOnlySkillRoots(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	skillRoot := t.TempDir() // outside the workspace
	m := NewManager(&config.Config{Cwd: ws})
	m.AddReadOnlyRoot(skillRoot)

	skillFile := filepath.Join(skillRoot, "my-skill", "SKILL.md")

	// Read-only tools pass without a prompt.
	for _, tool := range []string{"Read", "ReadMany", "DescribeImage"} {
		if got := m.UnapprovedOutDomainPaths(tool, map[string]any{"file_path": skillFile}); len(got) != 0 {
			t.Fatalf("%s under a skill root should need no approval, got %v", tool, got)
		}
	}
	for _, tool := range []string{"Glob", "Grep"} {
		if got := m.UnapprovedOutDomainPaths(tool, map[string]any{"path": skillRoot}); len(got) != 0 {
			t.Fatalf("%s under a skill root should need no approval, got %v", tool, got)
		}
	}

	// Writes under the same root still prompt.
	if got := m.UnapprovedOutDomainPaths("Write", map[string]any{"file_path": skillFile}); len(got) != 1 {
		t.Fatalf("Write under a skill root must still need approval, got %v", got)
	}
	// Reads outside the root still prompt.
	if got := m.UnapprovedOutDomainPaths("Read", map[string]any{"file_path": filepath.Join(t.TempDir(), "x.md")}); len(got) != 1 {
		t.Fatalf("Read outside a skill root must still need approval, got %v", got)
	}
}

func TestSetReadOnlyRootsReplaces(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	first, second := t.TempDir(), t.TempDir()
	m := NewManager(&config.Config{Cwd: ws})

	m.SetReadOnlyRoots([]string{first, second})
	f1, f2 := filepath.Join(first, "a.md"), filepath.Join(second, "b.md")
	for _, p := range []string{f1, f2} {
		if got := m.UnapprovedOutDomainPaths("Read", map[string]any{"file_path": p}); len(got) != 0 {
			t.Fatalf("read under %s should be prompt-free, got %v", p, got)
		}
	}
	// Replacing the set drops the first root.
	m.SetReadOnlyRoots([]string{second})
	if got := m.UnapprovedOutDomainPaths("Read", map[string]any{"file_path": f1}); len(got) != 1 {
		t.Fatalf("read under a removed root must prompt again, got %v", got)
	}
	if got := m.UnapprovedOutDomainPaths("Read", map[string]any{"file_path": f2}); len(got) != 0 {
		t.Fatalf("read under the kept root should stay prompt-free, got %v", got)
	}
}
