package slash

import (
	"strings"
	"testing"
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

func TestModelSwitchReportsThinking(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Cfg.ThinkingByModel = map[string]bool{"thinker:latest": true}
	if _, _, err := Dispatch(ctx, "/model thinker"); err != nil {
		t.Fatal(err)
	}
	if !ctx.Cfg.ThinkingKnown || !ctx.Cfg.ThinkingSupported {
		t.Fatal("expected thinking flags refreshed from cache")
	}
	if !strings.Contains(out.String(), "thinking: yes") {
		t.Fatalf("expected thinking word, got %q", out.String())
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
