package slash

import (
	"strings"
	"testing"
)

func TestModelSwitchRefreshesVision(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Cfg.VisionByModel = map[string]bool{"llava:latest": true}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/model llava"); err != nil {
		t.Fatal(err)
	}
	if !ctx.Cfg.VisionKnown || !ctx.Cfg.VisionAvailable {
		t.Fatal("expected vision flags to refresh from cache")
	}
	if !strings.Contains(out.String(), "vision: yes") {
		t.Fatalf("expected vision confirmation, got %q", out.String())
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/model plain-text-model"); err != nil {
		t.Fatal(err)
	}
	if ctx.Cfg.VisionKnown {
		t.Fatal("expected unknown vision for uncached model")
	}
	if !strings.Contains(out.String(), "vision: unknown") {
		t.Fatalf("expected unknown note, got %q", out.String())
	}
}
