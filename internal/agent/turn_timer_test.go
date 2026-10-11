package agent

import (
	"context"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// The "responded in" footer is driven by the UI turn timer. The runtime must
// restart that timer once per turn: previously it was only reset on the
// non-interactive input path, so an interactive session kept counting from
// startup and the footer grew across every user message.
func TestRunStartsUITurnTimer(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Model:         "m",
		ContextWindow: 32768,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ui := &fakeUI{}
	ag := New(cfg, fakeClient{}, reg, perm, sess, ui)

	if err := ag.Run(context.Background(), "say ok"); err != nil {
		t.Fatalf("run agent: %v", err)
	}
	if ui.startTurns != 1 {
		t.Fatalf("expected exactly one UI turn start, got %d", ui.startTurns)
	}

	if err := ag.Run(context.Background(), "say ok again"); err != nil {
		t.Fatalf("run agent again: %v", err)
	}
	if ui.startTurns != 2 {
		t.Fatalf("expected the turn timer to restart on the next turn, got %d turn starts", ui.startTurns)
	}
}
