package permissions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestUnattendedAutoApprovesAndDeniesDangerous(t *testing.T) {
	t.Parallel()

	m := NewManager(&config.Config{YesMode: false})
	if m.Unattended() {
		t.Fatal("new manager must not be unattended")
	}
	m.SetUnattended(true)
	if !m.Unattended() {
		t.Fatal("expected unattended on")
	}
	if !m.Check("Write", map[string]any{"file_path": "/tmp/a", "contents": "x"}, nil) {
		t.Fatal("unattended should auto-approve ask tools without a UI")
	}
	if !m.Check("WebFetch", map[string]any{"url": "https://example.com"}, nil) {
		t.Fatal("unattended should auto-approve network tools")
	}
	// Dangerous bash must be denied outright, never prompted (a prompt would
	// hang an unattended run).
	if m.Check("Bash", map[string]any{"command": "sudo rm -rf /tmp/x"}, fakeUI{decision: tui.DecisionAllowOnce}) {
		t.Fatal("unattended must deny always-confirm bash")
	}

	m.SetUnattended(false)
	if m.Check("Write", map[string]any{"file_path": "/tmp/a", "contents": "x"}, nil) {
		t.Fatal("without unattended, ask tool should be denied without a UI")
	}
}

func TestUnattendedRespectsPersistentDeny(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vibe-coder.env")
	if err := os.WriteFile(path, []byte("TOOL_PERMISSIONS={\"write\":\"deny\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := NewManager(&config.Config{PermFile: path})
	m.SetUnattended(true)
	if m.Check("Write", map[string]any{"file_path": "/tmp/a", "contents": "x"}, nil) {
		t.Fatal("unattended must respect a persistent deny")
	}
}

func TestMissionToolsAreSafeTier(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"MissionStart", "MissionComplete", "MissionBlocked"} {
		if toolTier(normalizeTool(name)) != TierSafe {
			t.Fatalf("%s should be safe tier so it never prompts", name)
		}
	}
}
