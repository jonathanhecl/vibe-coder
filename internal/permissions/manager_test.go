package permissions

import (
	"context"
	"errors"
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

func TestAlwaysConfirmSurvivesRememberedApproval(t *testing.T) {
	for _, name := range []string{"Bash", "InteractiveBash"} {
		m := NewManager(&config.Config{YesMode: true})
		m.AllowSession(name)
		if m.Check(name, map[string]any{"command": "sudo echo confirmation"}, fakeUI{decision: tui.DecisionDeny}) {
			t.Fatalf("%s remembered approval bypassed mandatory confirmation", name)
		}
	}
}

func TestPersistentDenyAppliesToSafeTools(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vibe-coder.env")
	if err := os.WriteFile(path, []byte("TOOL_PERMISSIONS={\"read\":\"deny\",\"subagent\":\"deny\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(&config.Config{PermFile: path, YesMode: true})
	for _, name := range []string{"Read", "SubAgent"} {
		if m.Check(name, nil, nil) {
			t.Fatalf("persistent deny ignored for %s", name)
		}
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

type mockSafetyDecider struct {
	dangerous bool
	err       error
	enabled   bool
}

func (m *mockSafetyDecider) IsCommandDangerous(ctx context.Context, command string) (bool, error) {
	return m.dangerous, m.err
}

func (m *mockSafetyDecider) Enabled() bool {
	return m.enabled
}

func TestAssistedExecutionMode(t *testing.T) {
	decider := &mockSafetyDecider{enabled: true}
	m := NewManager(&config.Config{YesMode: false, AssistedYes: true})
	m.SetSafetyDecider(decider)

	// 1. Safe command is auto-approved by assisted mode
	decider.dangerous = false
	if !m.Check("Bash", map[string]any{"command": "git status"}, nil) {
		t.Fatal("expected safe command to be auto-approved in assisted mode")
	}

	// 2. Dangerous command prompts user (denied when ui is nil)
	decider.dangerous = true
	if m.Check("Bash", map[string]any{"command": "rm -rf .git"}, nil) {
		t.Fatal("expected dangerous command to require user permission")
	}

	// 3. Dangerous command allowed when user approves via UI
	ui := fakeUI{decision: tui.DecisionAllowOnce}
	if !m.Check("Bash", map[string]any{"command": "rm -rf .git"}, ui) {
		t.Fatal("expected dangerous command to be allowed when user explicitly approves")
	}

	// 4. Always-confirm commands (e.g. sudo) always prompt even if decider returns false
	decider.dangerous = false
	if m.Check("Bash", map[string]any{"command": "sudo apt update"}, nil) {
		t.Fatal("expected always-confirm command (sudo) to require UI prompt even if decider claims safe")
	}

	// 5. Decider error falls through to prompt
	decider.err = errors.New("timeout")
	if m.Check("Bash", map[string]any{"command": "ls"}, nil) {
		t.Fatal("expected error from safety decider to safely fall through to prompt")
	}
	decider.err = nil

	// 6. When assisted mode is disabled, commands prompt UI
	m.SetAssistedMode(false)
	decider.dangerous = false
	if m.Check("Bash", map[string]any{"command": "git status"}, nil) {
		t.Fatal("expected bash command to require prompt when assisted mode is off")
	}
}
