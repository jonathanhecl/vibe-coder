package slash

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type fakeModelLister struct {
	models []ollama.Model
	err    error
}

func (f *fakeModelLister) Tags(context.Context) ([]ollama.Model, error) {
	return f.models, f.err
}

func pickerTestModels() []ollama.Model {
	return []ollama.Model{
		{Name: "qwen3.5:4b", Capabilities: []string{"tools", "thinking"}, CapabilitiesKnown: true},
		{Name: "llama3.2:3b", Capabilities: []string{"tools"}, CapabilitiesKnown: true},
		{Name: "moondream", Capabilities: []string{"vision"}, CapabilitiesKnown: true},
	}
}

func newPickerTestCtx(t *testing.T, answers ...string) (*Ctx, *config.Config, *bytes.Buffer) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "qwen3.5:4b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
		ConfigDir:     tmp,
		ConfigFile:    filepath.Join(tmp, "vibe-coder.env"),
		ToolsByModel:  map[string]bool{"qwen3.5:4b": true, "llama3.2:3b": true},
		VisionByModel: map[string]bool{"moondream": true},
	}
	out := &bytes.Buffer{}
	ctx := &Ctx{
		Cfg:      cfg,
		Session:  session.New(cfg),
		Out:      out,
		Models:   &fakeModelLister{models: pickerTestModels()},
		Prompter: &fakePrompter{answers: answers},
	}
	return ctx, cfg, out
}

func TestSidecarToggleCommands(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t)

	if _, _, err := Dispatch(ctx, "/sidecar off"); err != nil {
		t.Fatal(err)
	}
	if !cfg.SidecarSkipSession {
		t.Fatal("expected /sidecar off to disable the sidecar for this session")
	}
	if !strings.Contains(out.String(), "Sidecar disabled for this session") {
		t.Fatalf("expected disable confirmation, got %q", out.String())
	}

	if _, _, err := Dispatch(ctx, "/sidecar on"); err != nil {
		t.Fatal(err)
	}
	if cfg.SidecarSkipSession {
		t.Fatal("expected /sidecar on to re-enable the sidecar")
	}
}

func TestSidecarStatusCommand(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t)
	cfg.SidecarModel = "moondream"

	if _, _, err := Dispatch(ctx, "/sidecar status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Sidecar: on (moondream") {
		t.Fatalf("expected sidecar status line, got %q", out.String())
	}
}

func TestSidecarUnknownAction(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t)

	if _, _, err := Dispatch(ctx, "/sidecar 7"); err != nil {
		t.Fatal(err)
	}
	if cfg.SidecarSkipSession {
		t.Fatal("expected an unknown action to leave the sidecar untouched")
	}
	if !strings.Contains(out.String(), "Unknown /sidecar action") {
		t.Fatalf("expected unknown-action message, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Usage: /sidecar") {
		t.Fatalf("expected sidecar usage, got %q", out.String())
	}
}

func TestRemovedModelCommandIsUnknown(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t)

	handled, shouldExit, err := Dispatch(ctx, "/model")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /model result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if cfg.Model != "qwen3.5:4b" {
		t.Fatalf("expected the removed /model command to change nothing, got %q", cfg.Model)
	}
	if !strings.Contains(out.String(), "Unknown command: /model") {
		t.Fatalf("expected /model to be unknown, got %q", out.String())
	}
}

func TestSavePersistsModelSettings(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t)
	cfg.Model = "changed-model:v2"

	handled, shouldExit, err := Dispatch(ctx, "/save")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /save result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	data, err := os.ReadFile(cfg.ConfigFile)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	if !strings.Contains(string(data), "ROLE_MAIN_MODEL=changed-model:v2") {
		t.Fatalf("expected the main role model persisted, got:\n%s", data)
	}
	if strings.Contains(string(data), "\nMODEL=") {
		t.Fatalf("expected the legacy MODEL key to be dropped, got:\n%s", data)
	}
	if !strings.Contains(out.String(), "Saved") {
		t.Fatalf("expected save confirmation, got %q", out.String())
	}
}
