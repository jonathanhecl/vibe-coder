package permissions

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type decisionUI struct {
	decision tui.Decision
}

func (d decisionUI) AskPermission(string, map[string]any) tui.Decision { return d.decision }

func TestSetYesModeDenyAllAndAddAskTool(t *testing.T) {
	t.Parallel()

	m := NewManager(&config.Config{})
	m.SetYesMode(true)
	if !m.Check("Write", map[string]any{"file_path": "x", "contents": "y"}, nil) {
		t.Fatal("yes mode should allow write without UI")
	}

	m.DenyAll("Write")
	if m.Check("Write", map[string]any{"file_path": "x", "contents": "y"}, nil) {
		t.Fatal("deny-all should block write")
	}

	m.AddAskTool("CustomTool")
	if toolTier("customtool") != TierAsk {
		t.Fatal("custom tool should be ask tier")
	}
}

func TestDecisionBranches(t *testing.T) {
	t.Parallel()

	m := NewManager(&config.Config{})
	if !m.Check("Write", map[string]any{"file_path": "a", "contents": "b"}, decisionUI{decision: tui.DecisionAllowAll}) {
		t.Fatal("allow-all decision should allow")
	}
	if !m.Check("Write", map[string]any{"file_path": "a", "contents": "b"}, nil) {
		t.Fatal("persisted in-session allow should keep allowing")
	}

	m2 := NewManager(&config.Config{})
	if m2.Check("Edit", map[string]any{"file_path": "a", "old_string": "x", "new_string": "y"}, decisionUI{decision: tui.DecisionDenyAll}) {
		t.Fatal("deny-all decision should block")
	}
	if m2.Check("Edit", map[string]any{"file_path": "a", "old_string": "x", "new_string": "y"}, nil) {
		t.Fatal("persisted in-session deny should keep denying")
	}

	m3 := NewManager(&config.Config{})
	if !m3.Check("Bash", map[string]any{"command": "echo ok"}, decisionUI{decision: tui.DecisionYesMode}) {
		t.Fatal("yes-mode decision should allow")
	}
	if !m3.Check("Write", map[string]any{"file_path": "a", "contents": "b"}, nil) {
		t.Fatal("yes-mode should be enabled after DecisionYesMode")
	}
}

