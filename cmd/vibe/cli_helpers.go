package main

import (
	"os"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"golang.org/x/term"
)

func extractPersistDirective(args []string) ([]string, bool) {
	filtered := make([]string, 0, len(args))
	persist := false
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if strings.EqualFold(trimmed, "-save") || strings.EqualFold(trimmed, "--save") {
			persist = true
			continue
		}
		filtered = append(filtered, arg)
	}
	return filtered, persist
}

func shouldRunFirstRunOnboarding(cfg *config.Config, persistModelSettings bool) bool {
	if cfg == nil || persistModelSettings {
		return false
	}
	if cfg.ConfigFileExists {
		return false
	}
	if cfg.Prompt != "" || cfg.ListSessions || cfg.Resume {
		return false
	}
	if strings.TrimSpace(cfg.RAGIndex) != "" {
		return false
	}
	return stdioIsTTY()
}

func shouldContinueInteractiveAfterPrompt(cfg *config.Config, stdinTTY, stdoutTTY bool) bool {
	if cfg == nil || strings.TrimSpace(cfg.Prompt) == "" || !cfg.Interactive {
		return false
	}
	return stdinTTY && stdoutTTY
}

func stdioIsTTY() bool {
	// TTY checks gate interactive retry prompts and first-run setup.
	return stdinIsTTY() && stdoutIsTTY()
}

func stdinIsTTY() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func stdoutIsTTY() bool {
	return term.IsTerminal(int(os.Stdout.Fd()))
}
