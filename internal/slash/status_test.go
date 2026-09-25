package slash

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
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

func TestStatusAssistedReflectsEffectiveMode(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		JevstyleModel: "jev-decision:v1",
		AssistedYes:   true,
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
		PermFile:      filepath.Join(tmp, "permissions.json"),
	}
	s := session.New(cfg)
	perm := permissions.NewManager(cfg)

	// The manager starts assisted because the config enables it...
	if !perm.AssistedMode() {
		t.Fatal("expected the manager to start in assisted mode")
	}
	// ...but a JEV failure can disable it at runtime while cfg keeps ASSISTED_YES.
	perm.SetAssistedMode(false)
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Perm: perm, Agent: &fakePlanAgent{}, Out: out}
	writeStatus(ctx, tui.NewStyleForTest(false))
	if !strings.Contains(out.String(), "Assisted: off") {
		t.Fatalf("expected effective assisted off, got %q", out.String())
	}
	if strings.Contains(out.String(), "Assisted: on") {
		t.Fatalf("expected assisted not marked on, got %q", out.String())
	}

	perm.SetAssistedMode(true)
	out.Reset()
	writeStatus(ctx, tui.NewStyleForTest(false))
	if !strings.Contains(out.String(), "Assisted: on (JEV Style)") {
		t.Fatalf("expected effective assisted on, got %q", out.String())
	}
}

func TestStatusAssistedFallsBackToConfigWithoutManager(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		JevstyleModel: "jev-decision:v1",
		AssistedYes:   true,
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
	}
	s := session.New(cfg)
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakePlanAgent{}, Out: out}

	writeStatus(ctx, tui.NewStyleForTest(false))
	if !strings.Contains(out.String(), "Assisted: on (JEV Style)") {
		t.Fatalf("expected assisted on from config, got %q", out.String())
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
