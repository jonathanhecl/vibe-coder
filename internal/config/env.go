package config

import (
	"os"
	"strconv"
	"strings"
	"time"
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
	if v := strings.TrimSpace(envFirstNonEmpty("VIBE_CODER_HIDE_THINK", "VIBE_CODER_HIDE_THINKING")); v != "" {
		if b, ok := parseBoolish(v); ok {
			cfg.OllamaHideThink = b
		}
	}
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
