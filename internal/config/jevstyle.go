package config

import "strings"

// JevstyleInUse reports whether any JEV Style decider is configured, either the
// v1/v2 (Ollama-backed) model or the v3 endpoint.
func (c *Config) JevstyleInUse() bool {
	if c == nil {
		return false
	}
	return c.JevstyleOllamaInUse() || c.JevstyleV3InUse()
}

// JevstyleOllamaInUse reports whether the v1/v2 (Ollama-backed) decision model
// is configured. It deliberately ignores the v3 endpoint so callers that build
// an Ollama client do not run when only v3 is configured.
func (c *Config) JevstyleOllamaInUse() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.JevstyleModel) != ""
}

// JevstyleV3InUse reports whether a JEV Style v3 endpoint is configured.
func (c *Config) JevstyleV3InUse() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.JevstyleV3Endpoint) != ""
}
