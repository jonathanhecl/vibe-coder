package slash

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestThinkSetAndStatus(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)

	if _, _, err := Dispatch(ctx, "/think"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Thinking level: default") {
		t.Fatalf("expected default status, got %q", out.String())
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/think high"); err != nil {
		t.Fatal(err)
	}
	if ctx.Cfg.OllamaThinkLevel != "high" {
		t.Fatalf("expected level high, got %q", ctx.Cfg.OllamaThinkLevel)
	}
	if ctx.Cfg.OllamaNoThink {
		t.Fatal("setting a level must clear the legacy no-think flag")
	}
	if !strings.Contains(out.String(), "Thinking level set to: high") {
		t.Fatalf("expected confirmation, got %q", out.String())
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/think off"); err != nil {
		t.Fatal(err)
	}
	if ctx.Cfg.OllamaThinkLevel != "off" || !ctx.Cfg.OllamaNoThink {
		t.Fatalf("off must set level and legacy flag: %+v", ctx.Cfg)
	}
}

func TestThinkInvalidLevel(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	if _, _, err := Dispatch(ctx, "/think ultra"); err != nil {
		t.Fatal(err)
	}
	if ctx.Cfg.OllamaThinkLevel != "" {
		t.Fatalf("invalid level must not apply, got %q", ctx.Cfg.OllamaThinkLevel)
	}
	if !strings.Contains(out.String(), "Invalid think level") {
		t.Fatalf("expected usage error, got %q", out.String())
	}
}

func TestThinkCapabilitySuffix(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Cfg.Model = "thinker"
	ctx.Cfg.ThinkingByModel = map[string]bool{"thinker": true}
	if _, _, err := Dispatch(ctx, "/think low"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "thinker supports thinking") {
		t.Fatalf("expected capability note, got %q", out.String())
	}

	out.Reset()
	ctx.Cfg.ThinkingByModel = map[string]bool{"thinker": false}
	if _, _, err := Dispatch(ctx, "/think"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "has no thinking capability") {
		t.Fatalf("expected no-capability note, got %q", out.String())
	}
}

func TestStatusShowsThinking(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Cfg.OllamaThinkLevel = "medium"
	ctx.Cfg.ThinkingKnown = true
	ctx.Cfg.ThinkingSupported = true
	if _, _, err := Dispatch(ctx, "/status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Thinking: medium (supported)") {
		t.Fatalf("expected thinking line, got %q", out.String())
	}
}

// The thinking row must not imply an effort the main provider never receives.
func TestStatusReportsUndeliverableThinkingLevel(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	if err := ctx.Cfg.AddProvider(config.Provider{Name: "local", Endpoint: "http://127.0.0.1:8080/v1", Kind: config.ProviderOpenAI}); err != nil {
		t.Fatal(err)
	}
	if err := ctx.Cfg.SetRoleModel(config.RoleMain, "local", "swift-1.5"); err != nil {
		t.Fatal(err)
	}
	ctx.Cfg.OllamaThinkLevel = "low"
	if _, _, err := Dispatch(ctx, "/status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Thinking: low (not applied: main provider is openai)") {
		t.Fatalf("expected the provider caveat, got %q", out.String())
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/think"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Thinking level: low (not applied: main provider is openai)") {
		t.Fatalf("expected /think to report the same caveat, got %q", out.String())
	}
}

func TestModelSwitchReportsThinking(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	initTestRegistry(ctx.Cfg)
	ctx.Cfg.ThinkingByModel = map[string]bool{"thinker:latest": true}
	if _, _, err := Dispatch(ctx, "/setmodel main thinker:latest"); err != nil {
		t.Fatal(err)
	}
	if !ctx.Cfg.ThinkingKnown || !ctx.Cfg.ThinkingSupported {
		t.Fatal("expected thinking flags refreshed from cache")
	}
	if !strings.Contains(out.String(), "main role set to thinker:latest") {
		t.Fatalf("expected role confirmation, got %q", out.String())
	}
}

func TestHelpMentionsThink(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	if _, _, err := Dispatch(ctx, "/help"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/think") {
		t.Fatalf("expected /help to mention /think, got %q", out.String())
	}
}
