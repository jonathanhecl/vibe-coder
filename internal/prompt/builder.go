package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

const basePrompt = `You are vibe-coder, a local-first coding assistant.
Be concise, practical, and safe.
If a request is ambiguous, ask a short clarifying question.`

func Build(cfg *config.Config) string {
	shell := detectShell()

	var osPrompt string
	switch runtime.GOOS {
	case "windows":
		osPrompt = "Windows mode: prefer PowerShell-compatible commands."
	case "darwin":
		osPrompt = "macOS mode: use POSIX shell commands."
	default:
		osPrompt = "Linux mode: use POSIX shell commands."
	}

	parts := []string{
		basePrompt,
		"",
		"# Environment",
		fmt.Sprintf("- cwd: %s", cfg.Cwd),
		fmt.Sprintf("- platform: %s/%s", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("- shell: %s", shell),
		"",
		"# OS Notes",
		osPrompt,
	}

	projectInstr := loadProjectInstructions(cfg.Cwd)
	if len(projectInstr) > 0 {
		parts = append(parts, "", "# Project Instructions")
		parts = append(parts, projectInstr...)
	}
	return strings.Join(parts, "\n")
}

func detectShell() string {
	for _, key := range []string{"SHELL", "COMSPEC"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

func loadProjectInstructions(cwd string) []string {
	start, err := filepath.Abs(cwd)
	if err != nil {
		return nil
	}
	entries := make([]string, 0, 8)
	current := start
	for i := 0; i < 10; i++ {
		for _, name := range []string{".vibe-coder.json", "AGENTS.md"} {
			path := filepath.Join(current, name)
			info, err := os.Lstat(path)
			if err != nil || info.Mode()&os.ModeSymlink != 0 {
				continue
			}
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			if len(data) > 4000 {
				data = data[:4000]
			}
			clean := SanitizeInstructions(string(data))
			rel, _ := filepath.Rel(start, path)
			entries = append(entries, fmt.Sprintf("From %s:\n%s", relPath(rel), clean))
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return entries
}

func SanitizeInstructions(input string) string {
	out := input
	replacements := []*regexp.Regexp{
		regexp.MustCompile(`(?is)<invoke.*?>.*?</invoke>`),
		regexp.MustCompile(`(?is)<function=.*?>.*?</function>`),
		regexp.MustCompile(`(?is)<[A-Z_]+>.*?</[A-Z_]+>`),
	}
	for _, re := range replacements {
		out = re.ReplaceAllString(out, "[BLOCKED]")
	}
	return strings.TrimSpace(out)
}

func relPath(rel string) string {
	if rel == "" || rel == "." {
		return "."
	}
	return filepath.ToSlash(rel)
}
