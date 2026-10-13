package decision

import (
	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// FromConfig builds a decision client from the application configuration.
// It returns nil when no decision model or endpoint is configured.
func FromConfig(cfg *config.Config) *Client {
	if cfg == nil || !cfg.DecisionInUse() {
		return nil
	}
	var opts []Option
	if key := cfg.DecisionAPIKey; key != "" {
		opts = append(opts, WithAPIKey(key))
	}
	return New(cfg.DecisionEffectiveEndpoint(), cfg.DecisionModel, opts...)
}
