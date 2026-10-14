package slash

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// TestSaveInIsolatedSessionStaysInTheFolder pins the behaviour of /save inside
// an isolated run: the transcript and the settings land in the working
// directory, the mode does not change, and the user-level directories stay
// untouched.
func TestSaveInIsolatedSessionStaysInTheFolder(t *testing.T) {
	home := t.TempDir()
	projectDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VIBE_CODER_ISOLATED", "")
	t.Setenv("ISOLATED", "")
	t.Setenv("OLLAMA_HOST", "http://127.0.0.1:1")
	t.Setenv("VIBE_CODER_CONFIG", filepath.Join(home, "cfg.env"))
	t.Chdir(projectDir)

	cfg, err := config.Load([]string{"--isolated"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Isolated {
		t.Fatal("expected an isolated config")
	}

	s := session.New(cfg)
	s.AddUser("hello")
	var out bytes.Buffer
	ctx := &Ctx{Cfg: cfg, Session: s, Agent: &fakePlanAgent{}, Out: &out}

	handled, shouldExit, err := Dispatch(ctx, "/save")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /save result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Saved isolated session") {
		t.Errorf("expected the message to name the isolated session, got %q", out.String())
	}
	if !strings.Contains(out.String(), "still isolated in "+projectDir) {
		t.Errorf("expected the message to say where the session stays isolated, got %q", out.String())
	}

	sessionPath := filepath.Join(projectDir, config.IsolatedSessionFileName)
	info, err := os.Stat(sessionPath)
	if err != nil {
		t.Fatalf("expected the transcript at %s: %v", sessionPath, err)
	}
	if info.Size() == 0 {
		t.Errorf("expected a non-empty transcript at %s", sessionPath)
	}
	if !session.HasIsolatedSession(projectDir) {
		t.Error("expected the folder to keep detecting the isolated session")
	}
	if _, err := os.Stat(filepath.Join(projectDir, config.IsolatedConfigFileName)); err != nil {
		t.Errorf("expected the folder settings file: %v", err)
	}
	if !cfg.Isolated {
		t.Error("saving must not leave the isolated mode")
	}
	for _, name := range []string{".config", ".local"} {
		if _, err := os.Stat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("isolated /save must not create %s under the home directory", name)
		}
	}
}
