package main

import (
	"context"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
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
