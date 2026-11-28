package slash

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// /autocontinue toggles a session-only flag and reports its own state; an
// unknown argument leaves the current state untouched.
func TestAutoContinueCommandTogglesSessionFlag(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: tmp}
	var out bytes.Buffer
	c := &Ctx{Cfg: cfg, Out: &out}

	handled, exit, err := Dispatch(c, "/autocontinue on")
	if err != nil || !handled || exit {
		t.Fatalf("unexpected /autocontinue on result: handled=%t exit=%t err=%v", handled, exit, err)
	}
	if !cfg.AutoContinue {
		t.Fatal("/autocontinue on must enable the flag")
	}
	if !strings.Contains(out.String(), "Auto-continue on") {
		t.Fatalf("unexpected output: %q", out.String())
	}

	out.Reset()
	if _, _, err := Dispatch(c, "/autocontinue"); err != nil {
		t.Fatalf("unexpected status error: %v", err)
	}
	if !strings.Contains(out.String(), "Auto-continue: on") {
		t.Fatalf("status must report on: %q", out.String())
	}

	handled, exit, err = Dispatch(c, "/autocontinue off")
	if err != nil || !handled || exit {
		t.Fatalf("unexpected /autocontinue off result: handled=%t exit=%t err=%v", handled, exit, err)
	}
	if cfg.AutoContinue {
		t.Fatal("/autocontinue off must disable the flag")
	}

	out.Reset()
	if _, _, err := Dispatch(c, "/autocontinue maybe"); err != nil {
		t.Fatalf("an invalid value must not be an error: %v", err)
	}
	if cfg.AutoContinue {
		t.Fatal("an invalid value must not change the flag")
	}
	if !strings.Contains(out.String(), "Invalid value") {
		t.Fatalf("expected usage guidance, got %q", out.String())
	}
}
