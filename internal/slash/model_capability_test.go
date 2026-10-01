package slash

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// fakeModelInspector lists models and answers /api/show, so the live
// capability probe can be exercised without a network.
type fakeModelInspector struct {
	models []ollama.Model
	shown  map[string]ollama.Model
}

func (f *fakeModelInspector) Tags(context.Context) ([]ollama.Model, error) {
	return f.models, nil
}

func (f *fakeModelInspector) Show(_ context.Context, name string) (ollama.Model, error) {
	if m, ok := f.shown[name]; ok {
		return m, nil
	}
	return ollama.Model{}, errors.New("model not found")
}

// TestModelSwitchProbesCapabilitiesLive covers the reported case: the exact
// tag advertises vision/thinking, but a stale startup snapshot only knew a
// sibling tag that lacks them. The switch must re-check the model live and
// report the real capabilities instead of the sibling's.
func TestModelSwitchProbesCapabilitiesLive(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "gemma:q4_k_m",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
		ConfigDir:     tmp,
		ConfigFile:    tmp + "/vibe-coder.env",
		// Stale snapshot: the sibling tag has tools only.
		VisionByModel:   map[string]bool{"gemma:q4_k_m": false},
		ThinkingByModel: map[string]bool{"gemma:q4_k_m": false},
		ToolsByModel:    map[string]bool{"gemma:q4_k_m": true},
	}
	out := &bytes.Buffer{}
	ctx := &Ctx{
		Cfg: cfg,
		Out: out,
		Models: &fakeModelInspector{
			models: []ollama.Model{
				{Name: "gemma:q4_k_m", Capabilities: []string{"tools", "completion"}, CapabilitiesKnown: true},
			},
			shown: map[string]ollama.Model{
				"gemma:fixed": {Name: "gemma:fixed", Capabilities: []string{"completion", "vision", "audio", "tools", "thinking"}, CapabilitiesKnown: true},
			},
		},
	}

	if _, _, err := Dispatch(ctx, "/model gemma:fixed"); err != nil {
		t.Fatal(err)
	}
	if !cfg.VisionKnown || !cfg.VisionAvailable {
		t.Fatalf("expected vision yes after live probe, got known=%t available=%t", cfg.VisionKnown, cfg.VisionAvailable)
	}
	if !cfg.ThinkingKnown || !cfg.ThinkingSupported {
		t.Fatalf("expected thinking yes after live probe, got known=%t supported=%t", cfg.ThinkingKnown, cfg.ThinkingSupported)
	}
	if !cfg.ToolsKnown || !cfg.ToolsSupported {
		t.Fatalf("expected native tools after live probe, got known=%t supported=%t", cfg.ToolsKnown, cfg.ToolsSupported)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "vision: yes") || !strings.Contains(rendered, "thinking: yes") {
		t.Fatalf("expected honest capability report, got %q", rendered)
	}
}

// TestModelSwitchFallsBackWhenProbeFails keeps the previous behavior when the
// host cannot be inspected: cached values are used, and an absent tag stays
// unknown rather than borrowing a sibling.
func TestModelSwitchFallsBackWhenProbeFails(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:           "gemma:q4_k_m",
		ContextWindow:   32768,
		Cwd:             tmp,
		SessionsDir:     tmp,
		ConfigDir:       tmp,
		ConfigFile:      tmp + "/vibe-coder.env",
		VisionByModel:   map[string]bool{"gemma:q4_k_m": false},
		ThinkingByModel: map[string]bool{"gemma:q4_k_m": false},
		ToolsByModel:    map[string]bool{"gemma:q4_k_m": true},
	}
	out := &bytes.Buffer{}
	ctx := &Ctx{
		Cfg: cfg,
		Out: out,
		Models: &fakeModelInspector{
			models: []ollama.Model{
				{Name: "gemma:q4_k_m", Capabilities: []string{"tools", "completion"}, CapabilitiesKnown: true},
			},
			shown: map[string]ollama.Model{}, // every Show fails
		},
	}

	if _, _, err := Dispatch(ctx, "/model gemma:fixed"); err != nil {
		t.Fatal(err)
	}
	if cfg.VisionKnown || cfg.ThinkingKnown {
		t.Fatalf("expected unknown when the probe fails and the tag is absent, got vision=%t/%t thinking=%t/%t",
			cfg.VisionKnown, cfg.VisionAvailable, cfg.ThinkingKnown, cfg.ThinkingSupported)
	}
	if !strings.Contains(out.String(), "vision: unknown") {
		t.Fatalf("expected unknown vision, got %q", out.String())
	}
}
