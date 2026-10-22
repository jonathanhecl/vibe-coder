package tui

import (
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// ThinkingRow renders the configured thinking effort for the main model. The
// startup banner and /status share it so the level is reported in the same
// words in both places, including the reason it may not reach the model.
func ThinkingRow(cfg *config.Config) Row {
	level, note := thinkingLevelAndNote(cfg)
	plain := level
	if note != "" {
		plain += " (" + note + ")"
	}
	return Row{
		Label: "Thinking",
		Plain: plain,
		Render: func(st Style) string {
			value := st.Gray(level)
			switch level {
			case "off":
				value = st.Yellow(level)
			case "default":
			default:
				value = st.Cyan(level)
			}
			if note == "" {
				return value
			}
			return value + " " + st.Gray("("+note+")")
		},
	}
}

// thinkingLevelAndNote resolves the effective level ("default" when nothing is
// configured, "off" for the legacy no-think flag) plus a short note explaining
// whether the model or the provider can honor it.
func thinkingLevelAndNote(cfg *config.Config) (string, string) {
	if cfg == nil {
		return "default", ""
	}
	level := strings.TrimSpace(cfg.OllamaThinkLevel)
	if level == "" {
		level = "default"
	}
	if cfg.OllamaNoThink && level == "default" {
		level = "off"
	}
	// The provider decides whether the level is even delivered, so it outranks
	// what the model advertises.
	if note := ThinkingProviderNote(cfg); note != "" {
		return level, note
	}
	switch {
	case cfg.ThinkingKnown && cfg.ThinkingSupported:
		return level, "supported"
	case cfg.ThinkingKnown:
		return level, "not supported by the model"
	}
	return level, ""
}

// ThinkingProviderNote explains why a configured level cannot reach the main
// model: an OpenAI-compatible endpoint takes no `think` field and vibe sends no
// reasoning effort there, so /think only changes what the Ollama roles receive.
// It returns "" when the level is deliverable or when nothing is configured.
func ThinkingProviderNote(cfg *config.Config) string {
	if cfg == nil {
		return ""
	}
	if strings.TrimSpace(cfg.OllamaThinkLevel) == "" && !cfg.OllamaNoThink {
		return ""
	}
	status := cfg.RoleStatus(config.RoleMain)
	provider, ok := cfg.Provider(status.Provider)
	if !ok || provider.Kind != config.ProviderOpenAI {
		return ""
	}
	return "not applied: main provider is openai"
}
