package main

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// testRegistryConfig builds a config with the registry initialized, without
// going through Load (which would touch the real config directory).
func testRegistryConfig() *config.Config {
	return &config.Config{
		OllamaHost: "http://localhost:11434",
		Model:      "main-model",
		Providers: map[string]config.Provider{
			"ollama": {Name: "ollama", Kind: config.ProviderOllama, Endpoint: "http://localhost:11434"},
			"groq":   {Name: "groq", Kind: config.ProviderOpenAI, Endpoint: "https://api.groq.com/openai/v1"},
		},
		RoleModels: map[config.Role]config.RoleModel{
			config.RoleMain:    {Provider: "ollama", Model: "main-model"},
			config.RoleSidecar: {Provider: "groq", Model: "side-model"},
		},
	}
}

// S14: main and sidecar on one provider must reuse a single client.
func TestReuseClientTarget(t *testing.T) {
	t.Parallel()
	if !reuseClientTarget("http://localhost:11434", "http://localhost:11434", false, false) {
		t.Fatal("expected one shared client for the same endpoint and protocol")
	}
	if reuseClientTarget("http://localhost:11434", "http://localhost:11434", false, true) {
		t.Fatal("different protocols must not share a client")
	}
	if reuseClientTarget("http://localhost:11434", "http://localhost:1234", false, false) {
		t.Fatal("different endpoints must not share a client")
	}
}

func TestVisionClientForReusesRoleClients(t *testing.T) {
	t.Parallel()
	mainClient := ollama.NewHTTP("http://localhost:11434")
	sideClient := ollama.NewHTTP("https://api.groq.com/openai/v1")

	// Unbound role: no client.
	cfg := testRegistryConfig()
	if got := visionClientFor(cfg, mainClient, sideClient); got != nil {
		t.Fatalf("expected no client without a bound vision role, got %+v", got)
	}
	if role := newVisionRole(cfg, mainClient, sideClient); role != nil {
		t.Fatalf("expected no describer without a bound vision role, got %+v", role)
	}

	// Bound to the main provider: reuse the main client.
	cfg.RoleModels[config.RoleVision] = config.RoleModel{Provider: "ollama", Model: "llava:latest"}
	got := visionClientFor(cfg, mainClient, sideClient)
	if got != mainClient {
		t.Fatalf("expected the main client to be reused, got %+v", got)
	}
	role := newVisionRole(cfg, mainClient, sideClient)
	if role == nil || !role.Enabled() || role.Model() != "llava:latest" {
		t.Fatalf("expected a describer for llava:latest, got %+v", role)
	}

	// Bound to the sidecar provider: reuse the sidecar client.
	cfg.RoleModels[config.RoleVision] = config.RoleModel{Provider: "groq", Model: "llama-3.2-11b-vision"}
	if got := visionClientFor(cfg, mainClient, sideClient); got != sideClient {
		t.Fatalf("expected the sidecar client to be reused, got %+v", got)
	}

	// Bound to a third provider: a dedicated client, never the shared ones.
	cfg.Providers["local"] = config.Provider{Name: "local", Kind: config.ProviderOllama, Endpoint: "http://localhost:8080"}
	cfg.RoleModels[config.RoleVision] = config.RoleModel{Provider: "local", Model: "moondream"}
	got = visionClientFor(cfg, mainClient, sideClient)
	if got == nil || got == mainClient || got == sideClient {
		t.Fatalf("expected a dedicated client for the third provider, got %+v", got)
	}

	// A provider without an endpoint cannot be used.
	cfg.Providers["empty"] = config.Provider{Name: "empty", Kind: config.ProviderOllama}
	cfg.RoleModels[config.RoleVision] = config.RoleModel{Provider: "empty", Model: "moondream"}
	if got := visionClientFor(cfg, mainClient, sideClient); got != nil {
		t.Fatalf("expected no client for a provider without an endpoint, got %+v", got)
	}
}
