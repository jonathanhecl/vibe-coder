package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

const defaultChatRequestTimeout = 15 * time.Minute

// EffectiveChatTimeout returns the deadline used for a single Ollama /api/chat turn
// (main agent and SubAgent). When ChatTimeout is zero or unset, 15 minutes is used.
func (c *Config) EffectiveChatTimeout() time.Duration {
	if c != nil && c.ChatTimeout > 0 {
		return c.ChatTimeout
	}
	return defaultChatRequestTimeout
}

func applyEnv(cfg *Config) {
	if v := strings.TrimSpace(os.Getenv("OLLAMA_HOST")); v != "" {
		cfg.OllamaHost = v
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_MODEL", "VIBEGO_MODEL")); v != "" {
		cfg.Model = v
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_UI", "VIBEGO_UI")); v != "" {
		cfg.UI = v
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_SIDECAR_MODEL", "VIBEGO_SIDECAR_MODEL")); v != "" {
		cfg.SidecarModel = v
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_JEVSTYLE_MODEL", "VIBEGO_JEVSTYLE_MODEL", "VIBE_JEVSTYLE_MODEL", "JEVSTYLE_MODEL")); v != "" {
		cfg.JevstyleModel = v
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_SIDECAR_DISABLED")); v != "" {
		if b, ok := parseBoolish(v); ok {
			cfg.SidecarDisabled = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_SIDECAR_ENABLED")); v != "" {
		if b, ok := parseBoolish(v); ok {
			cfg.SidecarDisabled = !b
		}
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_DEBUG", "VIBEGO_DEBUG")); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.Debug = parsed
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_CHAT_TIMEOUT")); v != "" {
		if parsed, err := time.ParseDuration(v); err == nil && parsed > 0 {
			cfg.ChatTimeout = parsed
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_NO_THINK")); v != "" {
		if b, ok := parseBoolish(v); ok && b {
			cfg.OllamaNoThink = true
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_THINK")); v != "" {
		if norm, ok := ollama.NormalizeThinkLevel(v); ok && norm != "" {
			cfg.OllamaThinkLevel = norm
		}
	}
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_HIDE_THINK", "VIBE_CODER_HIDE_THINKING")); v != "" {
		if b, ok := parseBoolish(v); ok {
			cfg.OllamaHideThink = b
		}
	}
	if v := strings.TrimSpace(os.Getenv("VIBE_CODER_CONTEXT")); v != "" {
		cfg.ContextFiles = append(cfg.ContextFiles, splitPathList(v)...)
	}
}

// splitPathList splits an env/config path list on the OS path-list
// separator, dropping empty entries.
func splitPathList(v string) []string {
	parts := strings.Split(v, string(os.PathListSeparator))
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// parseBoolish parses common truthy/falsey strings for env/config keys.
func parseBoolish(s string) (value bool, ok bool) {
	v := strings.ToLower(strings.TrimSpace(s))
	switch v {
	case "1", "true", "yes", "y", "on":
		return true, true
	case "0", "false", "no", "n", "off":
		return false, true
	default:
		return false, false
	}
}
