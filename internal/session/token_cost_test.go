package session

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// A native tool call carries its arguments outside Content. A Write of 40 KB is
// ~10k tokens the provider pays for, and leaving it out of the budget made long
// sessions look small until the provider rejected the request.
func TestMessageCostCountsToolArguments(t *testing.T) {
	t.Parallel()
	call := ollama.MessageToolCall{Function: ollama.MessageToolCallFunction{
		Name:      "Write",
		Arguments: map[string]any{"file_path": "/tmp/x", "content": strings.Repeat("x", 40000)},
	}}
	m := Message{Role: "assistant", ToolCalls: []ollama.MessageToolCall{call}}

	if got := MessageTokenCost(m.Content); got != 0 {
		t.Fatalf("fixture content must be empty, got %d", got)
	}
	if got := MessageCost(m); got < 9000 {
		t.Fatalf("tool arguments were not priced: %d", got)
	}
}

// The session's running estimate must include them too, or compaction never
// triggers on a session made of big tool calls.
func TestSessionEstimateIncludesToolArguments(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{ContextWindow: 32768, Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := New(cfg)
	s.AddAssistantToolCalls("", []ollama.MessageToolCall{{
		Function: ollama.MessageToolCallFunction{
			Name:      "Edit",
			Arguments: map[string]any{"old_string": strings.Repeat("a", 20000), "new_string": strings.Repeat("b", 20000)},
		},
	}})

	if got := s.TokenEstimate(); got < 9000 {
		t.Fatalf("session estimate ignores tool arguments: %d", got)
	}
}
