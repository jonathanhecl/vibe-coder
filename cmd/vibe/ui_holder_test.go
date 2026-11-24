package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/slash"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestUIHolderAndHotSwap(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("VIBE_CODER_CONFIG", cfgPath)

	cfg := &config.Config{
		UI:            "plain",
		Model:         "qwen3:8b",
		ContextWindow: 8192,
		Cwd:           tmp,
		ConfigDir:     tmp,
		ConfigFile:    cfgPath,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	reg := tools.NewRegistry()
	perm := permissions.NewManager(cfg)
	sess := session.New(cfg)
	initialUI, err := tui.NewFromMode(cfg)
	if err != nil {
		t.Fatalf("NewFromMode: %v", err)
	}
	uiBox := newUIHolder(initialUI)
	defer uiBox.Stop()

	ag := agent.New(cfg, nil, reg, perm, sess, initialUI)
	var out bytes.Buffer
	slashCtx := &slash.Ctx{
		Cfg:      cfg,
		Session:  sess,
		Perm:     perm,
		Agent:    ag,
		Out:      &out,
		Prompter: uiBox.Get(),
	}
	slashCtx.OnUIChange = makeUISwitcher(cfg, ag, sess, reg, uiBox, slashCtx)

	if shouldExit := handleInputLine(context.Background(), slashCtx, ag, uiBox.Get(), "/ui tui"); shouldExit {
		t.Fatal("did not expect exit on /ui tui")
	}
	if _, ok := uiBox.Get().(*tui.HybridUI); !ok {
		t.Fatalf("expected active UI to swap to *tui.HybridUI, got %T", uiBox.Get())
	}
	if _, ok := slashCtx.Prompter.(*tui.HybridUI); !ok {
		t.Fatalf("expected slashCtx.Prompter to update to *tui.HybridUI, got %T", slashCtx.Prompter)
	}
	data, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(data), "UI=tui") {
		t.Fatalf("expected UI=tui persisted in %s, got %q err=%v", cfgPath, string(data), err)
	}

	out.Reset()
	if shouldExit := handleInputLine(context.Background(), slashCtx, ag, uiBox.Get(), "/ui cli"); shouldExit {
		t.Fatal("did not expect exit on /ui cli")
	}
	if _, ok := uiBox.Get().(*tui.PlainUI); !ok {
		t.Fatalf("expected active UI to swap back to *tui.PlainUI, got %T", uiBox.Get())
	}
	data2, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(data2), "UI=plain") {
		t.Fatalf("expected UI=plain persisted in %s, got %q err=%v", cfgPath, string(data2), err)
	}
}
