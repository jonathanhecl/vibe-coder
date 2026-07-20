package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCLI_AddAndList(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// Create a dummy source skill file
	sourceFile := filepath.Join(tmpDir, "my-skill-src.md")
	content := "This is a custom skill instruction\nLine 2 content."
	if err := os.WriteFile(sourceFile, []byte(content), 0o644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	// 1. Add local skill
	addLocalArgs := []string{"add", "custom-skill", sourceFile}
	if err := RunCLI(configDir, cwd, addLocalArgs); err != nil {
		t.Fatalf("RunCLI add local failed: %v", err)
	}

	// Verify file was created locally
	localPath := filepath.Join(cwd, ".vibe-coder", "skills", "custom-skill.md")
	data, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("failed to read copied local skill file: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected copied content %q, got %q", content, string(data))
	}

	// 2. Add global skill
	addGlobalArgs := []string{"add", "--global", "global-skill", sourceFile}
	if err := RunCLI(configDir, cwd, addGlobalArgs); err != nil {
		t.Fatalf("RunCLI add global failed: %v", err)
	}

	// Verify file was created globally
	globalPath := filepath.Join(configDir, "skills", "global-skill.md")
	data, err = os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("failed to read copied global skill file: %v", err)
	}
	if string(data) != content {
		t.Errorf("expected copied content %q, got %q", content, string(data))
	}

	// 3. List skills
	listArgs := []string{"list"}
	if err := RunCLI(configDir, cwd, listArgs); err != nil {
		t.Fatalf("RunCLI list failed: %v", err)
	}
}

func TestRunCLI_Errors(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	// Missing args
	err := RunCLI(configDir, cwd, []string{"add"})
	if err == nil {
		t.Fatal("expected error with missing args, got nil")
	}
	if !strings.Contains(err.Error(), "missing <name> or <source_file_path>") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Source file does not exist
	err = RunCLI(configDir, cwd, []string{"add", "test-skill", "nonexistent.md"})
	if err == nil {
		t.Fatal("expected error with nonexistent source file, got nil")
	}

	// Invalid skill name
	err = RunCLI(configDir, cwd, []string{"add", "test/skill", "nonexistent.md"})
	if err == nil {
		t.Fatal("expected error with invalid skill name, got nil")
	}
	if !strings.Contains(err.Error(), "invalid skill name") {
		t.Errorf("unexpected error message: %v", err)
	}

	// Unknown subcommand
	err = RunCLI(configDir, cwd, []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error with unknown subcommand, got nil")
	}
	if !strings.Contains(err.Error(), "unknown skill subcommand") {
		t.Errorf("unexpected error message: %v", err)
	}
}
