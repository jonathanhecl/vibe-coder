package prompt

import (
	"fmt"
	"os"
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

	return strings.Join([]string{
		basePrompt,
		"",
		"# Environment",
		fmt.Sprintf("- cwd: %s", cfg.Cwd),
		fmt.Sprintf("- platform: %s/%s", runtime.GOOS, runtime.GOARCH),
		fmt.Sprintf("- shell: %s", shell),
		"",
		"# OS Notes",
		osPrompt,
	}, "\n")
}

func detectShell() string {
	for _, key := range []string{"SHELL", "COMSPEC"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return "unknown"
}

