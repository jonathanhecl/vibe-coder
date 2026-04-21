package config

import "time"

const defaultChatRequestTimeout = 15 * time.Minute

// EffectiveChatTimeout returns the deadline used for a single Ollama /api/chat turn
// (main agent and SubAgent). When ChatTimeout is zero or unset, 15 minutes is used.
func (c *Config) EffectiveChatTimeout() time.Duration {
	if c != nil && c.ChatTimeout > 0 {
		return c.ChatTimeout
	}
	return defaultChatRequestTimeout
}
