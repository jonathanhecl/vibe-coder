package permissions

import (
	"os"
	"path/filepath"
	"strings"
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

func TestPersistentRulesLoadAndSave(t *testing.T) {
	tmp := t.TempDir()
	permFile := filepath.Join(tmp, "permissions.json")
	if err := os.WriteFile(permFile, []byte(`{"write":"allow","bash":"allow","edit":"deny"}`), 0o644); err != nil {
		t.Fatalf("write seed permissions: %v", err)
	}

	m := NewManager(&config.Config{PermFile: permFile})
	if !m.Check("Write", map[string]any{}, nil) {
		t.Fatalf("expected write allowed from persistent rules")
	}
	if m.Check("Edit", map[string]any{}, nil) {
		t.Fatalf("expected edit denied from persistent rules")
	}
	// Bash allow should never persist, so without yes-mode and no UI it remains denied.
	if m.Check("Bash", map[string]any{"command": "echo ok"}, nil) {
		t.Fatalf("expected bash not allowed from persisted rule")
	}

	m.AllowAll("Write")
	raw, err := os.ReadFile(permFile)
	if err != nil {
		t.Fatalf("read permissions file: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), `"bash":"allow"`) {
		t.Fatalf("bash allow must not be persisted: %s", string(raw))
	}
}
