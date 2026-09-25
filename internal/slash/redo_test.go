package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

func TestRedoRePrintsLastAssistantResponse(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: tmp}
	s := session.New(cfg)
	s.AddUser("quiero el clima de uruguay")
	s.AddAssistant("El clima es el siguiente:\n\n- Montevideo: 23º / 15º.\n- Salto: 30º / 16º.\n")

	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Out: out}

	handled, shouldExit, err := Dispatch(ctx, "/redo")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /redo result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "quiero el clima de uruguay") {
		t.Fatalf("expected the last user prompt for context, got %q", rendered)
	}
	if !strings.Contains(rendered, "assistant >") || !strings.Contains(rendered, "Montevideo: 23") {
		t.Fatalf("expected the last assistant response, got %q", rendered)
	}
	// Whitespace must survive the re-render: each item stays on its own line.
	if !strings.Contains(rendered, "Montevideo: 23º / 15º.\n") {
		t.Fatalf("expected preserved line breaks, got %q", rendered)
	}
}

func TestRedoWithoutAssistantResponse(t *testing.T) {
	cfg := &config.Config{}
	s := session.New(cfg)
	out := &bytes.Buffer{}
	ctx := &Ctx{Cfg: cfg, Session: s, Out: out}

	if _, _, err := Dispatch(ctx, "/redo"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No previous assistant response") {
		t.Fatalf("expected an empty-session message, got %q", out.String())
	}
}
