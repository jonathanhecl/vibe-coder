package permissions

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestCheckTimeoutDeniesAndTellsTheModelWhy(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws})
	target := filepath.Join(ws, "a.txt")

	if m.Check("Write", map[string]any{"file_path": target, "contents": "x"}, fakeUI{decision: tui.DecisionTimeout}) {
		t.Fatal("a timed-out prompt must not approve the action")
	}
	if m.WasCancelled() {
		t.Fatal("a timeout is a denial, not a cancel: the run must keep going")
	}
	reason := m.LastDenialReason()
	if !strings.Contains(reason, "timed out") {
		t.Fatalf("denial reason should name the timeout, got %q", reason)
	}
	if !strings.Contains(reason, "NOT executed") {
		t.Fatalf("denial reason should tell the model the action did not run, got %q", reason)
	}
}

func TestTimeoutDoesNotRememberADenialForTheSession(t *testing.T) {
	t.Parallel()
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws})
	target := filepath.Join(ws, "a.txt")
	write := map[string]any{"file_path": target, "contents": "x"}

	if m.Check("Write", write, fakeUI{decision: tui.DecisionTimeout}) {
		t.Fatal("first call should time out into a denial")
	}
	// A timeout records no rule, so the next prompt is a real question again and
	// can still be approved once the user is back.
	if !m.Check("Write", write, fakeUI{decision: tui.DecisionAllowOnce}) {
		t.Fatal("a timeout must not block the tool for the rest of the session")
	}
}
