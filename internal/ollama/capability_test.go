package ollama

import "testing"

func TestModelSupportsCapabilityNames(t *testing.T) {
	t.Parallel()
	chat := Model{Name: "qwen3.5:9b", Capabilities: []string{"completion", "tools", "thinking"}}
	if !chat.SupportsCompletion() {
		t.Fatal("expected completion support")
	}
	if chat.SupportsEmbedding() {
		t.Fatal("did not expect embedding support")
	}
	if chat.SupportsDecision() {
		t.Fatal("did not expect decision support")
	}

	alias := Model{Name: "legacy", Capabilities: []string{"chat"}}
	if !alias.SupportsCompletion() {
		t.Fatal("expected chat to be accepted as a completion alias")
	}

	embed := Model{Name: "nomic-embed-text", Capabilities: []string{"embedding"}}
	if !embed.SupportsEmbedding() {
		t.Fatal("expected embedding support")
	}
	if embed.SupportsCompletion() {
		t.Fatal("did not expect completion support for an embedding model")
	}

	decider := Model{Name: "systemone", Capabilities: []string{"decision", "completion"}}
	if !decider.SupportsDecision() {
		t.Fatal("expected decision support")
	}

	empty := Model{}
	for _, capability := range []string{"completion", "embedding", "decision"} {
		if empty.Supports(capability) {
			t.Fatalf("did not expect %s support for an empty model", capability)
		}
	}
	if empty.Supports("") {
		t.Fatal("did not expect an empty capability to match")
	}
}

func TestModelSupportsIsCaseAndSpaceInsensitive(t *testing.T) {
	t.Parallel()
	m := Model{Name: "x", Capabilities: []string{" Completion ", "EMBEDDING"}}
	if !m.Supports("completion") {
		t.Fatal("expected a trimmed, lowercased match")
	}
	if !m.Supports(" embedding ") {
		t.Fatal("expected a trimmed, lowercased match")
	}
	if m.Supports("tools") {
		t.Fatal("did not expect an unlisted capability to match")
	}
}
