package config

import (
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// DecisionInUse reports whether a decision model is configured, either as an
// Ollama model name (resolved against the Ollama host) or as a custom
// System One endpoint.
func (c *Config) DecisionInUse() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.DecisionModel) != "" ||
		strings.TrimSpace(c.DecisionEndpoint) != ""
}

// DecisionEffectiveEndpoint returns the configured decision endpoint, or the
// normalized Ollama host when only a model name is set.
func (c *Config) DecisionEffectiveEndpoint() string {
	if c == nil {
		return ""
	}
	if ep := strings.TrimSpace(c.DecisionEndpoint); ep != "" {
		return ep
	}
	return ollama.NormalizeURL(c.OllamaHost)
}
