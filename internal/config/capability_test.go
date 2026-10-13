package config

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func TestCheckModelForRoleOllamaVerified(t *testing.T) {
	t.Parallel()
	chat := ollama.Model{Name: "qwen3.5:9b", Capabilities: []string{"completion", "tools"}, CapabilitiesKnown: true}
	status := CheckModelForRole(ProviderOllama, RoleMain, chat)
	if status.Verdict != CapabilityOK || !status.Available || !status.Verified {
		t.Fatalf("expected a verified ok verdict, got %+v", status)
	}
	if status.Required != CapabilityCompletion {
		t.Fatalf("unexpected required capability: %q", status.Required)
	}

	embed := ollama.Model{Name: "nomic-embed-text", Capabilities: []string{"embedding"}, CapabilitiesKnown: true}
	status = CheckModelForRole(ProviderOllama, RoleMain, embed)
	if status.Verdict != CapabilityUnsupported || status.Available {
		t.Fatalf("expected an unsupported verdict for a chat role, got %+v", status)
	}
	status = CheckModelForRole(ProviderOllama, RoleEmbedding, embed)
	if status.Verdict != CapabilityOK || !status.Available {
		t.Fatalf("expected the embedding role to accept an embedding model, got %+v", status)
	}

	decider := ollama.Model{Name: "systemone", Capabilities: []string{"decision"}, CapabilitiesKnown: true}
	if status := CheckModelForRole(ProviderOllama, RoleDecision, decider); status.Verdict != CapabilityOK {
		t.Fatalf("expected the decision role to accept a decision model, got %+v", status)
	}
	if status := CheckModelForRole(ProviderOllama, RoleDecision, chat); status.Verdict != CapabilityUnsupported {
		t.Fatalf("expected a chat-only model to fail the decision role, got %+v", status)
	}

	seer := ollama.Model{Name: "llava", Capabilities: []string{"completion", "vision"}, CapabilitiesKnown: true}
	if status := CheckModelForRole(ProviderOllama, RoleVision, seer); status.Verdict != CapabilityOK {
		t.Fatalf("expected the vision role to accept a vision model, got %+v", status)
	}
	if status := CheckModelForRole(ProviderOllama, RoleVision, chat); status.Verdict != CapabilityUnsupported {
		t.Fatalf("expected a non-vision model to fail the vision role, got %+v", status)
	}
}

func TestCheckModelForRoleUnverifiedWhenCapabilitiesUnknown(t *testing.T) {
	t.Parallel()
	unknown := ollama.Model{Name: "mystery", CapabilitiesKnown: false}
	status := CheckModelForRole(ProviderOllama, RoleMain, unknown)
	if status.Verdict != CapabilityUnverified || !status.Available || status.Verified {
		t.Fatalf("expected an unverified but available verdict, got %+v", status)
	}
}

func TestCheckModelForRoleTrustsNonOllamaProviders(t *testing.T) {
	t.Parallel()
	// An OpenAI-compatible provider reports capabilities heuristically, so its
	// data is never treated as authoritative.
	model := ollama.Model{Name: "gpt-4o-mini", Capabilities: []string{"tools"}, CapabilitiesKnown: true}
	status := CheckModelForRole(ProviderOpenAI, RoleMain, model)
	if status.Verdict != CapabilityUnverified || !status.Available || status.Verified {
		t.Fatalf("expected an unverified but available verdict, got %+v", status)
	}
}

func TestCheckModelForRoleWithoutRequirement(t *testing.T) {
	t.Parallel()
	status := CheckModelForRole(ProviderOllama, Role("unknown"), ollama.Model{})
	if status.Verdict != CapabilityNotRequired || !status.Available || status.Required != "" {
		t.Fatalf("expected a not-required verdict, got %+v", status)
	}
}
