package permissions

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

type mockNetworkDecider struct {
	enabled      bool
	isDangerous  bool
	networkCheck func(command string) (bool, error)
}

func (m *mockNetworkDecider) IsCommandDangerous(ctx context.Context, command string) (bool, error) {
	return m.isDangerous, nil
}

func (m *mockNetworkDecider) Enabled() bool {
	return m.enabled
}

func (m *mockNetworkDecider) IsCommandNetworkAccess(ctx context.Context, command string) (bool, error) {
	if m.networkCheck != nil {
		return m.networkCheck(command)
	}
	return false, nil
}

func TestOfflineModeBlocksTierNetwork(t *testing.T) {
	cfg := &config.Config{YesMode: true}
	m := NewManager(cfg)

	// Without offline mode, network tools pass in yes mode
	if !m.Check("WebSearch", map[string]any{"query": "golang"}, nil) {
		t.Fatal("expected WebSearch allowed when offline mode is off")
	}
	if !m.Check("WebFetch", map[string]any{"url": "https://example.com"}, nil) {
		t.Fatal("expected WebFetch allowed when offline mode is off")
	}

	m.SetOfflineMode(true)
	if !m.OfflineMode() {
		t.Fatal("expected offline mode active")
	}

	for _, tool := range []string{"WebSearch", "WebFetch", "HTTPRequest"} {
		if m.Check(tool, map[string]any{"url": "https://example.com"}, nil) {
			t.Fatalf("expected %s blocked in offline mode", tool)
		}
		if !strings.Contains(m.LastDenialReason(), "Blocked by offline mode: network tools are disabled") {
			t.Fatalf("unexpected denial reason for %s: %s", tool, m.LastDenialReason())
		}
	}
}

func TestOfflineModeBlocksExternalCommandsAndAllowsLocal(t *testing.T) {
	cfg := &config.Config{YesMode: true}
	m := NewManager(cfg)
	m.SetOfflineMode(true)

	// External commands should be blocked
	blockedCommands := []string{
		"curl -s https://api.openai.com/v1/models",
		"wget https://example.com/file.tar.gz",
		"git clone https://github.com/jonathanhecl/vibe-coder.git",
		"npm install express",
		"pip install requests",
		"ssh user@remote.server.com",
	}

	for _, cmd := range blockedCommands {
		if m.Check("Bash", map[string]any{"command": cmd}, nil) {
			t.Fatalf("expected command %q to be blocked in offline mode", cmd)
		}
		if !strings.Contains(m.LastDenialReason(), "Blocked by offline mode: external network access is disabled") {
			t.Fatalf("unexpected denial reason for %q: %s", cmd, m.LastDenialReason())
		}
	}

	// Local loopback commands should NOT be blocked by offline mode
	localCommands := []string{
		"curl -s http://localhost:11434/api/tags",
		"curl -s http://127.0.0.1:8080/health",
		"curl -s http://[::1]:3000/api",
		"ls -la",
		"go test ./...",
	}

	for _, cmd := range localCommands {
		if !m.Check("Bash", map[string]any{"command": cmd}, nil) {
			t.Fatalf("expected local command %q to be allowed in offline mode", cmd)
		}
	}
}

func TestOfflineModeDelegatesToSafetyDecider(t *testing.T) {
	cfg := &config.Config{YesMode: true}
	m := NewManager(cfg)
	m.SetOfflineMode(true)

	decider := &mockNetworkDecider{
		enabled: true,
		networkCheck: func(command string) (bool, error) {
			if strings.Contains(command, "my-custom-fetch") {
				return true, nil
			}
			return false, nil
		},
	}
	m.SetSafetyDecider(decider)

	// Custom tool flagged by decider
	if m.Check("Bash", map[string]any{"command": "my-custom-fetch data"}, nil) {
		t.Fatal("expected custom fetch command blocked by the decision model")
	}
	if !strings.Contains(m.LastDenialReason(), "external network access is disabled") {
		t.Fatalf("unexpected denial reason: %s", m.LastDenialReason())
	}

	// Custom command not flagged by decider
	if !m.Check("Bash", map[string]any{"command": "custom-internal-script"}, nil) {
		t.Fatal("expected non-network custom command allowed")
	}
}
