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

func TestModelCommandListsAndSelectsByNumber(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t, "2")

	handled, shouldExit, err := Dispatch(ctx, "/model")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /model result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if cfg.Model != "llama3.2:3b" {
		t.Fatalf("expected model switched by number, got %q", cfg.Model)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Installed models (3)") {
		t.Fatalf("expected numbered model list, got %q", rendered)
	}
	if !strings.Contains(rendered, "[2] llama3.2:3b") {
		t.Fatalf("expected entry [2], got %q", rendered)
	}
	if !strings.Contains(rendered, "(current)") {
		t.Fatalf("expected current model marker, got %q", rendered)
	}
}

func TestSidecarCommandListsAndSelectsByNumber(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t, "3")

	handled, shouldExit, err := Dispatch(ctx, "/sidecar")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /sidecar result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if cfg.SidecarModel != "moondream" {
		t.Fatalf("expected sidecar switched by number, got %q", cfg.SidecarModel)
	}
	if !strings.Contains(out.String(), "Disable Sidecar") {
		t.Fatalf("expected a disable option for the sidecar, got %q", out.String())
	}
}

func TestSidecarCommandDisableOption(t *testing.T) {
	ctx, cfg, _ := newPickerTestCtx(t, "0")

	if _, _, err := Dispatch(ctx, "/sidecar"); err != nil {
		t.Fatal(err)
	}
	if !cfg.SidecarSkipSession {
		t.Fatal("expected sidecar disabled for the session")
	}
}

func TestJevstyleCommandListsAndSelectsByNumber(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t, "1")

	handled, shouldExit, err := Dispatch(ctx, "/jevstyle")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /jevstyle result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if cfg.JevstyleModel != "qwen3.5:4b" {
		t.Fatalf("expected JEV Style switched by number, got %q", cfg.JevstyleModel)
	}
	if !strings.Contains(out.String(), "Installed models (3)") {
		t.Fatalf("expected numbered model list, got %q", out.String())
	}
}

func TestPickerEnterKeepsCurrent(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t, "")

	if _, _, err := Dispatch(ctx, "/model"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "qwen3.5:4b" {
		t.Fatalf("expected Enter to keep the current model, got %q", cfg.Model)
	}
	if !strings.Contains(out.String(), "Model unchanged") {
		t.Fatalf("expected unchanged confirmation, got %q", out.String())
	}
}

func TestPickerAcceptsModelName(t *testing.T) {
	ctx, cfg, _ := newPickerTestCtx(t, "plain-text-model")

	if _, _, err := Dispatch(ctx, "/model"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "plain-text-model" {
		t.Fatalf("expected typed model name to be applied, got %q", cfg.Model)
	}
}

func TestModelCommandByNumericArgumentResolvesFromList(t *testing.T) {
	ctx, cfg, _ := newPickerTestCtx(t)

	if _, _, err := Dispatch(ctx, "/model 2"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "llama3.2:3b" {
		t.Fatalf("expected /model 2 to pick the second listed model, got %q", cfg.Model)
	}
}

func TestNumericArgumentOutOfRangeIsRejected(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t)

	if _, _, err := Dispatch(ctx, "/model 99"); err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "qwen3.5:4b" {
		t.Fatalf("expected out-of-range number to leave the model unchanged, got %q", cfg.Model)
	}
	if !strings.Contains(out.String(), "No model number 99") {
		t.Fatalf("expected out-of-range message, got %q", out.String())
	}
}

func TestPickerFallsBackToCachedModelsWithoutLister(t *testing.T) {
	ctx, cfg, out := newPickerTestCtx(t, "2")
	ctx.Models = nil

	if _, _, err := Dispatch(ctx, "/model"); err != nil {
		t.Fatal(err)
	}
	// Cached names are sorted alphabetically: llama3.2:3b, moondream, qwen3.5:4b.
	if cfg.Model != "moondream" {
		t.Fatalf("expected cached-list selection, got %q", cfg.Model)
	}
	if !strings.Contains(out.String(), "Installed models (3)") {
		t.Fatalf("expected cached model list, got %q", out.String())
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
	if !strings.Contains(string(data), "MODEL=changed-model:v2") {
		t.Fatalf("expected MODEL persisted, got:\n%s", data)
	}
	if !strings.Contains(out.String(), "Saved") {
		t.Fatalf("expected save confirmation, got %q", out.String())
	}
}
