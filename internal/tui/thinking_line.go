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
	// The model decides whether it can honor the level: the provider is no
	// longer a limit, since an OpenAI-compatible endpoint receives it as
	// reasoning_effort (or the template's enable_thinking switch).
	switch {
	case cfg.ThinkingKnown && cfg.ThinkingSupported:
		return level, "supported"
	case cfg.ThinkingKnown:
		return level, "not supported by the model"
	}
	return level, ""
}
