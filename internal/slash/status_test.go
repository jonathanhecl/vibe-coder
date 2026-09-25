package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestStatusAliases(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Model: "llama3.2:3b", ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)

	for _, cmd := range []string{"/status", "/info", "/stats"} {
		out := &bytes.Buffer{}
		ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakePlanAgent{}, Out: out}
		handled, shouldExit, err := Dispatch(ctx, cmd)
		if err != nil || !handled || shouldExit {
			t.Fatalf("%s: handled=%t exit=%t err=%v", cmd, handled, shouldExit, err)
		}
		if !strings.Contains(out.String(), "Model: llama3.2:3b") {
			t.Fatalf("%s: expected model line, got %q", cmd, out.String())
		}
	}
}

func TestStatusStyledUsesBannerLayout(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:           "llama3.2:3b",
		OllamaHost:      "http://mac-mini.local:11434",
		UI:              "rich",
		OllamaHideThink: true,
		ContextWindow:   32768,
		Cwd:             tmp,
		SessionsDir:     tmp,
	}
	s := session.New(cfg)
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakePlanAgent{}, Out: out}

	writeStatus(ctx, tui.NewStyleForTest(true))
	rendered := out.String()
	if !strings.Contains(rendered, "\x1b[") {
		t.Fatalf("expected ANSI styling, got %q", rendered)
	}
	for _, want := range []string{"status", "llama3.2:3b", "http://mac-mini.local:11434", "rich", "Hide think", "on"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in styled status, got %q", want, rendered)
		}
	}
}

func TestStatusShowsHiddenThinkingAndUI(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:           "llama3.2:3b",
		UI:              "rich",
		OllamaHideThink: true,
		ContextWindow:   32768,
		Cwd:             tmp,
		SessionsDir:     tmp,
	}
	s := session.New(cfg)
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakePlanAgent{}, Out: out}

	writeStatus(ctx, tui.NewStyleForTest(false))
	rendered := out.String()
	if !strings.Contains(rendered, "UI: rich") {
		t.Fatalf("expected UI line, got %q", rendered)
	}
	if !strings.Contains(rendered, "Hide think: on") {
		t.Fatalf("expected hidden-thinking line, got %q", rendered)
	}
}
