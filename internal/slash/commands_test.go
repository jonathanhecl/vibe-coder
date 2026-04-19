package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

func TestDispatchMinimumCommands(t *testing.T) {
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

	handled, shouldExit, err := Dispatch(ctx, "/yes")
	if err != nil || !handled || shouldExit || !cfg.YesMode {
		t.Fatalf("unexpected /yes result: handled=%t exit=%t err=%v yes=%t", handled, shouldExit, err, cfg.YesMode)
	}

	handled, shouldExit, err = Dispatch(ctx, "/no")
	if err != nil || !handled || shouldExit || cfg.YesMode {
		t.Fatalf("unexpected /no result: handled=%t exit=%t err=%v yes=%t", handled, shouldExit, err, cfg.YesMode)
	}

	s.AddUser("hello")
	handled, shouldExit, err = Dispatch(ctx, "/status")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /status result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Model:") {
		t.Fatalf("expected status output, got %q", out.String())
	}
}
