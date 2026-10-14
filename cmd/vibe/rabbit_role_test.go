package main

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func TestRabbitClientForUsesBoundProvider(t *testing.T) {
	t.Parallel()
	mainClient := ollama.NewHTTP("http://localhost:11434")
	sideClient := ollama.NewHTTP("https://api.groq.com/openai/v1")

	cfg := testRegistryConfig()
	if got := rabbitClientFor(cfg, mainClient, sideClient); got != nil {
		t.Fatalf("expected no rabbit client when the role is unbound, got %+v", got)
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
	if got := rabbitClientFor(cfg, mainClient, sideClient); got != nil {
		t.Fatalf("expected no rabbit client for a provider without an endpoint, got %+v", got)
	}
}

// The documented chain is rabbit, then sidecar, then main: read-only retrieval
// must run on the sidecar when the rabbit role is not usable, and only fall back
// to the sub-agent's own main client when there is no sidecar either.
func TestFastRetrievalForFallsBackToSidecarThenMain(t *testing.T) {
	t.Parallel()
	mainClient := ollama.NewHTTP("http://localhost:11434")
	sideClient := ollama.NewHTTP("https://api.groq.com/openai/v1")

	cfg := testRegistryConfig()

	client, model := fastRetrievalFor(cfg, mainClient, sideClient)
	if client != sideClient || model != "side-model" {
		t.Fatalf("expected the sidecar to serve retrieval, got %+v %q", client, model)
	}

	// A rabbit bound to a third provider wins over the sidecar.
	cfg.Providers["local"] = config.Provider{Name: "local", Kind: config.ProviderOllama, Endpoint: "http://localhost:8080"}
	cfg.RoleModels[config.RoleRabbit] = config.RoleModel{Provider: "local", Model: "rabbit-local"}
	if _, model := fastRetrievalFor(cfg, mainClient, sideClient); model != "rabbit-local" {
		t.Fatalf("expected the bound rabbit model, got %q", model)
	}

	// A rabbit whose provider has no endpoint degrades to the sidecar.
	cfg.Providers["empty"] = config.Provider{Name: "empty", Kind: config.ProviderOllama}
	cfg.RoleModels[config.RoleRabbit] = config.RoleModel{Provider: "empty", Model: "rabbit-empty"}
	if client, model := fastRetrievalFor(cfg, mainClient, sideClient); client != sideClient || model != "side-model" {
		t.Fatalf("expected the sidecar fallback, got %+v %q", client, model)
	}

	// Without a sidecar the chain lands on main, which the sub-agent already
	// uses, so no override is configured.
	delete(cfg.RoleModels, config.RoleRabbit)
	delete(cfg.RoleModels, config.RoleSidecar)
	if client, model := fastRetrievalFor(cfg, mainClient, sideClient); client != nil || model != "" {
		t.Fatalf("expected no override when the chain lands on main, got %+v %q", client, model)
	}

	if client, model := fastRetrievalFor(nil, mainClient, sideClient); client != nil || model != "" {
		t.Fatalf("expected no override without a config, got %+v %q", client, model)
	}
}
