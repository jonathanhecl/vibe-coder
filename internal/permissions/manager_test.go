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
	if !m.Check("TodoWrite", map[string]any{
		"todos": []any{
			map[string]any{"id": "step-1", "content": "plan", "status": "pending"},
		},
	}, nil) {
		t.Fatalf("TodoWrite should be treated as safe and allowed by default")
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

func TestAllowSessionDoesNotWritePermFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(cfgPath, []byte("MODEL=test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(&config.Config{PermFile: cfgPath})
	if !m.Check("Write", map[string]any{"file_path": "a", "contents": "b"}, fakeUI{decision: tui.DecisionAllowSession}) {
		t.Fatal("expected allow-session to permit write")
	}
	raw, _ := os.ReadFile(cfgPath)
	if strings.Contains(string(raw), config.ToolPermissionsKey) {
		t.Fatalf("allow-session must not write TOOL_PERMISSIONS, got %q", string(raw))
	}
	m2 := NewManager(&config.Config{PermFile: cfgPath})
	if m2.Check("Write", map[string]any{"file_path": "a", "contents": "b"}, nil) {
		t.Fatal("expected fresh manager to deny write without persisted rule")
	}
}

func TestCancelSetsWasCancelled(t *testing.T) {
	t.Parallel()
	m := NewManager(&config.Config{})
	if m.Check("Write", map[string]any{"file_path": "a", "contents": "b"}, fakeUI{decision: tui.DecisionCancel}) {
		t.Fatal("cancel should deny")
	}
	if !m.WasCancelled() {
		t.Fatal("expected WasCancelled true")
	}
	if !m.Check("Write", map[string]any{"file_path": "a", "contents": "b"}, fakeUI{decision: tui.DecisionAllowOnce}) {
		t.Fatal("expected allow once after cancel")
	}
	if m.WasCancelled() {
		t.Fatal("WasCancelled should reset on next Check")
	}
}

func TestPersistentRulesLoadAndSave(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	seed := `MODEL=test
TOOL_PERMISSIONS={"write":"allow","bash":"allow","edit":"deny"}
`
	if err := os.WriteFile(cfgPath, []byte(seed), 0o644); err != nil {
		t.Fatalf("write seed config: %v", err)
	}

	m := NewManager(&config.Config{PermFile: cfgPath})
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
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config file: %v", err)
	}
	if strings.Contains(strings.ToLower(string(raw)), `"bash":"allow"`) {
		t.Fatalf("bash allow must not be persisted: %s", string(raw))
	}
	if !strings.Contains(string(raw), "TOOL_PERMISSIONS=") {
		t.Fatalf("expected TOOL_PERMISSIONS in vibe-coder.env")
	}
}
