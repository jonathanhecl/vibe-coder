package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func writeTestSkill(t *testing.T, root, name, frontmatter, body string, files ...string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := body
	if frontmatter != "" {
		content = "---\n" + frontmatter + "\n---\n" + body
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestSkillToolLoadsFullContent(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := writeTestSkill(t, root, "pdf-tools", "name: pdf-tools\ndescription: manipulate PDFs",
		"# PDF tools\n\nRun the script with --flag.\n", "scripts/convert.sh")
	tool := NewSkillTool(&config.Config{Cwd: t.TempDir(), ConfigDir: t.TempDir(), SkillsDirs: []string{root}})

	res := tool.Execute(context.Background(), map[string]any{"name": "PDF-Tools"})
	if res.IsError {
		t.Fatalf("expected skill content, got error: %s", res.Output)
	}
	for _, want := range []string{"Skill: pdf-tools (skills_dirs:", "Base directory: " + dir, "Run the script with --flag.", "Bundled files", "convert.sh"} {
		if !strings.Contains(res.Output, want) {
			t.Fatalf("expected %q in output, got %q", want, res.Output)
		}
	}
	if strings.Contains(res.Output, "description: manipulate PDFs") {
		t.Fatal("frontmatter must be stripped from the body")
	}
}

func TestSkillToolUnknownNameListsAvailable(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestSkill(t, root, "alpha", "", "body")
	writeTestSkill(t, root, "beta", "", "body")
	tool := NewSkillTool(&config.Config{Cwd: t.TempDir(), ConfigDir: t.TempDir(), SkillsDirs: []string{root}})

	res := tool.Execute(context.Background(), map[string]any{"name": "gamma"})
	if !res.IsError || !strings.Contains(res.Output, "alpha") || !strings.Contains(res.Output, "beta") {
		t.Fatalf("expected available names, got %+v", res)
	}
	if res := tool.Execute(context.Background(), map[string]any{"name": " "}); !res.IsError {
		t.Fatal("expected error for blank name")
	}
}

func TestSkillToolRespectsOutputCap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestSkill(t, root, "big", "", strings.Repeat("line\n", 2000))
	tool := NewSkillTool(&config.Config{Cwd: t.TempDir(), ConfigDir: t.TempDir(), SkillsDirs: []string{root}})
	tool.SetMaxOutputBytes(1024)

	res := tool.Execute(context.Background(), map[string]any{"name": "big"})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if len(res.Output) > 2048 || !strings.Contains(res.Output, "truncated") {
		t.Fatalf("expected truncated output, got %d bytes", len(res.Output))
	}
}
