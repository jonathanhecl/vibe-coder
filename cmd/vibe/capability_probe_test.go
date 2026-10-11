package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/openai"
)

type probingClient struct {
	emptyRetryClient
	tagCaps  []string
	showCaps []string
	showErr  error
}

func (c probingClient) Tags(context.Context) ([]ollama.Model, error) {
	return []ollama.Model{{
		Name:              "probe:latest",
		Capabilities:      c.tagCaps,
		CapabilitiesKnown: len(c.tagCaps) > 0,
	}}, nil
}

func (c probingClient) Show(context.Context, string) (ollama.Model, error) {
	return ollama.Model{
		Name:              "probe:latest",
		Capabilities:      c.showCaps,
		CapabilitiesKnown: len(c.showCaps) > 0,
	}, c.showErr
}

func TestCapabilityProbeKeepsUnknownAsUnknown(t *testing.T) {
	cfg := &config.Config{Model: "probe:latest"}
	resolveVisionSupport(cfg, probingClient{})
	if cfg.ToolsKnown || cfg.ToolsSupported {
		t.Fatalf("absent capability metadata must stay unknown, got known=%t supported=%t", cfg.ToolsKnown, cfg.ToolsSupported)
	}
	if cfg.VisionKnown {
		t.Fatal("vision must stay unknown when tags omit capabilities and no inspector is available")
	}
}

func TestCapabilityProbeUsesShowForActiveModel(t *testing.T) {
	cfg := &config.Config{Model: "probe:latest", SidecarModel: "probe:latest"}
	resolveVisionSupport(cfg, probingClient{showCaps: []string{"completion", "tools", "vision"}})
	if !cfg.ToolsKnown || !cfg.ToolsSupported {
		t.Fatalf("expected tools support from /api/show, got known=%t supported=%t", cfg.ToolsKnown, cfg.ToolsSupported)
	}
	if !cfg.VisionKnown || !cfg.VisionAvailable {
		t.Fatalf("expected vision from /api/show, got known=%t available=%t", cfg.VisionKnown, cfg.VisionAvailable)
	}
	if !cfg.SidecarVisionKnown || !cfg.SidecarVisionAvailable {
		t.Fatal("sidecar should share the probed capability map")
	}
}

func TestCapabilityProbeRespectsTagsWhenKnown(t *testing.T) {
	// Tags say completion-only (known); Show must not wrongly re-enable tools.
	cfg := &config.Config{Model: "probe:latest"}
	resolveVisionSupport(cfg, probingClient{tagCaps: []string{"completion"}, showCaps: []string{"tools"}})
	if !cfg.ToolsKnown || cfg.ToolsSupported {
		t.Fatalf("tags-known model must not be reported as tool-capable: known=%t supported=%t", cfg.ToolsKnown, cfg.ToolsSupported)
	}
}

func TestResolveVisionSupportOpenAIMainWithOllamaSidecar(t *testing.T) {
	// 1. Mock OpenAI server returning a model with architecture.input_modalities: ["text", "image"]
	openaiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		data := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":     "local-coder-vision",
					"object": "model",
					"architecture": map[string]any{
						"input_modalities":  []string{"text", "image"},
						"output_modalities": []string{"text"},
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer openaiSrv.Close()

	// 2. Mock Ollama sidecar server returning tags with vision capability
	ollamaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/tags" {
			http.NotFound(w, r)
			return
		}
		data := map[string]any{
			"models": []map[string]any{
				{
					"name": "ollama-sidecar:latest",
					"details": map[string]any{
						"family": "moondream",
					},
					"capabilities": []string{"vision"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(data)
	}))
	defer ollamaSrv.Close()

	cfg := &config.Config{
		Model:        "local-coder-vision",
		SidecarModel: "ollama-sidecar:latest",
	}

	mainClient := openai.New(openaiSrv.URL, openai.WithHTTPClient(openaiSrv.Client()))
	sideClient := ollama.NewHTTP(ollamaSrv.URL)

	resolveVisionSupport(cfg, mainClient, sideClient)

	// Verify both main and sidecar are detected with vision enabled
	if !cfg.VisionKnown || !cfg.VisionAvailable {
		t.Fatalf("expected main OpenAI model to have vision available and known, got known=%t available=%t",
			cfg.VisionKnown, cfg.VisionAvailable)
	}
	if !cfg.SidecarVisionKnown || !cfg.SidecarVisionAvailable {
		t.Fatalf("expected sidecar Ollama model to have vision available and known, got known=%t available=%t",
			cfg.SidecarVisionKnown, cfg.SidecarVisionAvailable)
	}
}
