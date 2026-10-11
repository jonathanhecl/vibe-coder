package agent

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestOfflinePromptBlockInSystemPrompt(t *testing.T) {
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Offline:       false,
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	perm := permissions.NewManager(cfg)
	ag := New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})

	// Without offline mode
	prompt := ag.buildSystemPrompt()
	if strings.Contains(prompt, "# Offline Mode (enabled)") {
		t.Fatalf("expected no offline mode block when offline is disabled, got:\n%s", prompt)
	}

	// Toggle offline mode on via permissions manager
	perm.SetOfflineMode(true)
	prompt = ag.buildSystemPrompt()
	if !strings.Contains(prompt, "# Offline Mode (enabled)") {
		t.Fatalf("expected offline mode block when offline is enabled, got:\n%s", prompt)
	}
	if !strings.Contains(prompt, "External internet and network access is blocked") {
		t.Fatalf("expected offline details in system prompt, got:\n%s", prompt)
	}

	// Toggle offline mode off
	perm.SetOfflineMode(false)
	prompt = ag.buildSystemPrompt()
	if strings.Contains(prompt, "# Offline Mode (enabled)") {
		t.Fatalf("expected offline mode block removed when offline is disabled, got:\n%s", prompt)
	}
}
