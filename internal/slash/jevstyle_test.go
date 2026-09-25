package slash

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

func TestJevstyleSlashCommand(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
	}
	s := session.New(cfg)

	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Out:     &out,
	}

	// 1. Initial status with no model configured
	handled, shouldExit, err := Dispatch(ctx, "/jevstyle")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "no model configured") {
		t.Fatalf("expected 'no model configured', got %q", out.String())
	}

	// 2. Set JEV style model
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle jev-decision:v1")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle set result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if cfg.JevstyleModel != "jev-decision:v1" {
		t.Fatalf("expected JevstyleModel to be set, got %q", cfg.JevstyleModel)
	}
	if !strings.Contains(out.String(), "JEV Style model set to: jev-decision:v1") {
		t.Fatalf("expected set confirmation, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Tip: Enable assisted execution mode") {
		t.Fatalf("expected tip to enable assisted execution mode, got %q", out.String())
	}

	// 3. Inspect status via /jevstyle status
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle status")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle status result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "JEV Style: on (jev-decision:v1)") {
		t.Fatalf("expected active status, got %q", out.String())
	}

	// 4. Verify /status includes JEV Style model
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/status")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /status result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "JEV Style model: jev-decision:v1") {
		t.Fatalf("expected /status to include JEV Style model, got %q", out.String())
	}

	// 5. Invalid model name
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle invalid model!!")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle invalid result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Invalid model name format") {
		t.Fatalf("expected invalid format warning, got %q", out.String())
	}

	// 6. Disable JEV style model (should also deactivate assisted mode)
	cfg.AssistedYes = true
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle off")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle off result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if cfg.JevstyleModel != "" {
		t.Fatalf("expected JevstyleModel empty after off, got %q", cfg.JevstyleModel)
	}
	if cfg.AssistedYes {
		t.Fatal("expected AssistedYes false after /jevstyle off")
	}
	if !strings.Contains(out.String(), "disabled") {
		t.Fatalf("expected disabled confirmation, got %q", out.String())
	}

	// 7. /jevstyle test without model configured
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle test")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle test result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "no model configured") {
		t.Fatalf("expected 'no model configured', got %q", out.String())
	}

	// 8. /jevstyle test with mock client
	cfg.JevstyleModel = "jev-decision:v1"
	ctx.Client = &mockCommitClient{content: " B"}
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle test")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle test result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "JEV Style decision: B. No") {
		t.Fatalf("expected successful decision test, got %q", out.String())
	}

	// 9. /jevstyle assisted on / off
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle assisted on")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle assisted on result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !cfg.AssistedYes {
		t.Fatal("expected cfg.AssistedYes true")
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/jevstyle assisted off")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle assisted off result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if cfg.AssistedYes {
		t.Fatal("expected cfg.AssistedYes false")
	}

	// 10. /yes assisted
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/yes assisted")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /yes assisted result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !cfg.AssistedYes {
		t.Fatal("expected cfg.AssistedYes true after /yes assisted")
	}
	if !strings.Contains(out.String(), "Assisted yes mode enabled") {
		t.Fatalf("expected assisted mode message, got %q", out.String())
	}
}

func TestEnsureCommitTypePrefix(t *testing.T) {
	if got := ensureCommitTypePrefix("add new feature", "feat"); got != "feat: add new feature" {
		t.Fatalf("expected 'feat: add new feature', got %q", got)
	}
	if got := ensureCommitTypePrefix("fix: resolve bug", "feat"); got != "fix: resolve bug" {
		t.Fatalf("expected 'fix: resolve bug' preserved, got %q", got)
	}
	if got := ensureCommitTypePrefix("docs: update readme", "docs"); got != "docs: update readme" {
		t.Fatalf("expected 'docs: update readme' preserved, got %q", got)
	}
}

type mockCommitClient struct {
	content string
	err     error
}

func (m *mockCommitClient) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	if m.err != nil {
		return ollama.ChatResponse{}, m.err
	}
	return ollama.ChatResponse{Content: m.content}, nil
}
