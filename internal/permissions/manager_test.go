package permissions

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type fakeUI struct {
	decision tui.Decision
}

func (f fakeUI) AskPermission(_ string, _ map[string]any) tui.Decision {
	return f.decision
}

func TestCheckSafeAndAskTools(t *testing.T) {
	t.Parallel()

	m := NewManager(&config.Config{YesMode: false})
	if !m.Check("Read", map[string]any{"file_path": "/tmp/a.txt"}, nil) {
		t.Fatalf("safe tool should be allowed by default")
	}
	if m.Check("Write", map[string]any{"file_path": "/tmp/a.txt", "contents": "x"}, nil) {
		t.Fatalf("ask tool should be denied without UI and without -y")
	}
	if !m.Check("Write", map[string]any{"file_path": "/tmp/a.txt", "contents": "x"}, fakeUI{decision: tui.DecisionAllowOnce}) {
		t.Fatalf("ask tool should be allowed with positive UI decision")
	}
}

func TestAlwaysConfirmBashEvenWithYesMode(t *testing.T) {
	t.Parallel()
	m := NewManager(&config.Config{YesMode: true})
	ok := m.Check("Bash", map[string]any{"command": "sudo rm -rf /tmp/foo"}, fakeUI{decision: tui.DecisionDeny})
	if ok {
		t.Fatalf("expected always-confirm bash to be denied by UI decision")
	}
}
