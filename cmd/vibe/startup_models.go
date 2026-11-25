package main

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// startupConnectionFailed reports whether err is a transport-level failure:
// the request never got an HTTP response (dial error, reset, TLS failure) or
// it timed out. HTTP status errors, decode failures and session cancellation
// do not count — the provider answered, so the role is not "offline".
func startupConnectionFailed(err error) bool {
	var requestErr *url.Error
	return err != nil && !errors.Is(err, context.Canceled) &&
		(errors.Is(err, context.DeadlineExceeded) || errors.As(err, &requestErr))
}

// startupOfflineModels checks configured providers without loading any models.
// Roles already covered by the startup Tags calls reuse those results; every
// other bound role gets one bounded Version probe, run concurrently. The
// decision role is judged by its own startup Check result instead. The result
// only feeds the banner's "(offline)" marker: it is advisory, never persisted
// and never changes routing.
func startupOfflineModels(ctx context.Context, cfg *config.Config, main, side ollama.Client, checks map[ollama.Client]error, decisionErr error) map[config.Role]bool {
	offline := make(map[config.Role]bool)
	if startupConnectionFailed(decisionErr) {
		offline[config.RoleDecision] = true
	}
	providers := map[string]ollama.Client{
		cfg.RoleBinding(config.RoleMain).Provider: main,
	}
	if name := cfg.RoleBinding(config.RoleSidecar).Provider; name != "" {
		if _, exists := providers[name]; !exists {
			providers[name] = side
		}
	}
	pending := make(map[ollama.Client][]config.Role)
	for _, role := range []config.Role{config.RoleMain, config.RoleSidecar, config.RoleRabbit, config.RoleVision, config.RoleEmbedding} {
		if !cfg.RoleStatus(role).Bound {
			continue
		}
		var client ollama.Client
		switch role {
		case config.RoleMain:
			client = main
		case config.RoleSidecar:
			if cfg.SidecarDisabled || cfg.SidecarSkipSession {
				continue
			}
			client = side
		default:
			provider, ok := cfg.RoleProvider(role)
			if !ok || strings.TrimSpace(provider.Endpoint) == "" {
				continue
			}
			client = providers[provider.Name]
			if client == nil {
				client = createLLMClient(provider.Endpoint, provider.APIKey, provider.Kind == config.ProviderOpenAI)
				providers[provider.Name] = client
			}
		}
		if client == nil {
			continue
		}
		if err, checked := checks[client]; checked {
			if startupConnectionFailed(err) {
				offline[role] = true
			}
		} else {
			pending[client] = append(pending[client], role)
		}
	}
	if len(pending) == 0 {
		return offline
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	type result struct {
		roles []config.Role
		err   error
	}
	results := make(chan result, len(pending))
	for client, roles := range pending {
		go func(client ollama.Client, roles []config.Role) {
			_, err := client.Version(ctx)
			results <- result{roles: roles, err: err}
		}(client, roles)
	}
	for range pending {
		r := <-results
		if startupConnectionFailed(r.err) {
			for _, role := range r.roles {
				offline[role] = true
			}
		}
	}
	return offline
}
