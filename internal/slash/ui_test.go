package slash

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestUICommandSwitchAndPersist(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("VIBE_CODER_CONFIG", cfgPath)

	cfg := &config.Config{
		UI:         "plain",
		Model:      "qwen3:8b",
		ConfigDir:  tmp,
		ConfigFile: cfgPath,
	}
	var out bytes.Buffer
	var switched []string
	ctx := &Ctx{
		Cfg: cfg,
		Out: &out,
		OnUIChange: func(mode string) error {
			switched = append(switched, mode)
			return nil
		},
	}

	// No args prints current mode.
	handled, exit, err := Dispatch(ctx, "/ui")
	if !handled || exit || err != nil {
		t.Fatalf("unexpected /ui result: handled=%t exit=%t err=%v", handled, exit, err)
	}
	if !strings.Contains(out.String(), "plain (classic CLI)") {
		t.Fatalf("expected current UI mode output, got %q", out.String())
	}

	// Switch to tui and verify persistence.
	out.Reset()
	handled, exit, err = Dispatch(ctx, "/ui tui")
	if !handled || exit || err != nil {
		t.Fatalf("unexpected /ui tui result: handled=%t exit=%t err=%v", handled, exit, err)
	}
	if cfg.UI != "tui" || len(switched) != 1 || switched[0] != "tui" {
		t.Fatalf("expected switch to tui, cfg.UI=%q switched=%v", cfg.UI, switched)
	}
	raw, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(raw), "UI=tui") {
		t.Fatalf("expected UI=tui saved to %s, got %q err=%v", cfgPath, string(raw), err)
	}

	// Switch back to cli (normalizes to plain).
	out.Reset()
	_, _, err = Dispatch(ctx, "/ui cli")
	if err != nil {
		t.Fatalf("/ui cli failed: %v", err)
	}
	if cfg.UI != "plain" || len(switched) != 2 || switched[1] != "plain" {
		t.Fatalf("expected switch to plain, cfg.UI=%q switched=%v", cfg.UI, switched)
	}
	raw2, err := os.ReadFile(cfgPath)
	if err != nil || !strings.Contains(string(raw2), "UI=plain") {
		t.Fatalf("expected UI=plain saved to %s, got %q err=%v", cfgPath, string(raw2), err)
	}
}

func TestUICommandRollbackOnCallbackError(t *testing.T) {
	var out bytes.Buffer
	cfg := &config.Config{UI: "plain"}
	ctx := &Ctx{
		Cfg: cfg,
		Out: &out,
		OnUIChange: func(string) error {
			return errors.New("boom")
		},
	}

	_, _, err := Dispatch(ctx, "/ui tui")
	if err == nil {
		t.Fatal("expected error from OnUIChange")
	}
	if cfg.UI != "plain" {
		t.Fatalf("expected cfg.UI rollback to plain, got %q", cfg.UI)
	}

	// Invalid mode prints usage without error.
	out.Reset()
	_, _, err = Dispatch(ctx, "/ui neon")
	if err != nil || !strings.Contains(out.String(), "Invalid UI mode") {
		t.Fatalf("expected invalid mode message, got out=%q err=%v", out.String(), err)
	}
	if cfg.UI != "plain" {
		t.Fatalf("expected cfg.UI to remain plain, got %q", cfg.UI)
	}
}

func TestCommandCatalogIncludesUI(t *testing.T) {
	cat := CommandCatalog()
	found := false
	for _, entry := range cat {
		if entry[0] == "/ui" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected /ui in CommandCatalog, got %v", cat)
	}
}
