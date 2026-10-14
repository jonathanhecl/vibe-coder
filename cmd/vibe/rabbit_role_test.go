package main

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func TestRabbitClientForFallsBackToMainWhenUnbound(t *testing.T) {
	t.Parallel()
	mainClient := ollama.NewHTTP("http://localhost:11434")
	sideClient := ollama.NewHTTP("https://api.groq.com/openai/v1")

	cfg := testRegistryConfig()
	if got := rabbitClientFor(cfg, mainClient, sideClient); got != mainClient {
		t.Fatalf("expected main client fallback when rabbit is unbound, got %+v", got)
	}

	cfg.RoleModels[config.RoleRabbit] = config.RoleModel{Provider: "ollama", Model: "rabbit-model"}
	if got := rabbitClientFor(cfg, mainClient, sideClient); got != mainClient {
		t.Fatalf("expected main client reuse for the main provider, got %+v", got)
	}

	cfg.RoleModels[config.RoleRabbit] = config.RoleModel{Provider: "groq", Model: "rabbit-side"}
	if got := rabbitClientFor(cfg, mainClient, sideClient); got != sideClient {
		t.Fatalf("expected sidecar client reuse, got %+v", got)
	}

	cfg.Providers["local"] = config.Provider{Name: "local", Kind: config.ProviderOllama, Endpoint: "http://localhost:8080"}
	cfg.RoleModels[config.RoleRabbit] = config.RoleModel{Provider: "local", Model: "rabbit-local"}
	got := rabbitClientFor(cfg, mainClient, sideClient)
	if got == nil || got == mainClient || got == sideClient {
		t.Fatalf("expected a dedicated client for the third provider, got %+v", got)
	}

	cfg.Providers["empty"] = config.Provider{Name: "empty", Kind: config.ProviderOllama}
	cfg.RoleModels[config.RoleRabbit] = config.RoleModel{Provider: "empty", Model: "rabbit-empty"}
	if got := rabbitClientFor(cfg, mainClient, sideClient); got != mainClient {
		t.Fatalf("expected main fallback for a provider without an endpoint, got %+v", got)
	}
}