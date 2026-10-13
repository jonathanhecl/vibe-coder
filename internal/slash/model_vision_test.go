package slash

import (
	"strings"
	"testing"
)

func TestModelSwitchRefreshesVision(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	initTestRegistry(ctx.Cfg)
	ctx.Cfg.VisionByModel = map[string]bool{"llava:latest": true}
	ctx.Cfg.ToolsByModel = map[string]bool{"llava:latest": true}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/setmodel main llava:latest"); err != nil {
		t.Fatal(err)
	}
	if !ctx.Cfg.VisionKnown || !ctx.Cfg.VisionAvailable {
		t.Fatal("expected vision flags to refresh from cache")
	}
	if !strings.Contains(out.String(), "main role set to llava:latest") {
		t.Fatalf("expected role confirmation, got %q", out.String())
	}
	if !ctx.Cfg.ToolsKnown || !ctx.Cfg.ToolsSupported {
		t.Fatal("expected tools flags to refresh from cache")
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/setmodel main plain-text-model"); err != nil {
		t.Fatal(err)
	}
	if ctx.Cfg.VisionKnown {
		t.Fatal("expected unknown vision for uncached model")
	}
	if !strings.Contains(out.String(), "main role set to plain-text-model") {
		t.Fatalf("expected role confirmation, got %q", out.String())
	}
	if ctx.Cfg.ToolsKnown {
		t.Fatal("expected unknown tools for uncached model")
	}
}
