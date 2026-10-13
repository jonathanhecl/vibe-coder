package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newWindowTestAgent(t *testing.T) (*Agent, *session.Session) {
	t.Helper()
	cfg := &config.Config{
		Model:         "m",
		ContextWindow: 32768,
		MaxTokens:     32,
		Temperature:   0.1,
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	return New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{}), sess
}

func firstUserContent(msgs []ollama.Message) (string, bool) {
	for _, m := range msgs {
		if m.Role == "user" {
			return m.Content, true
		}
	}
	return "", false
}

// A single oversized tool result is larger than the transcript character
// budget, so the rolling window collapses to that one message. The wire
// request then carries no user query and strict OpenAI-compatible servers
// reject it with "No user query found in messages.".
func TestBuildOllamaMessagesKeepsUserAnchorForOversizedToolResult(t *testing.T) {
	t.Parallel()
	ag, sess := newWindowTestAgent(t)
	sess.AddUser("add a welcome line to the hide character")
	sess.AddAssistantToolCalls("", []ollama.MessageToolCall{{Function: ollama.MessageToolCallFunction{Name: "Read", Arguments: map[string]any{}}}})
	sess.AddToolResult("Read", strings.Repeat("x", 60000))

	msgs, _ := ag.buildOllamaMessages(context.Background(), "SYSTEM")

	if _, ok := firstUserContent(msgs); !ok {
		t.Fatalf("request carries no user message: %d messages, roles=%v", len(msgs), rolesOf(msgs))
	}
}

// When the budget cut lands past the only user turn, the window is made of
// assistant/tool messages only.
func TestBuildOllamaMessagesKeepsUserAnchorAfterFrontTruncation(t *testing.T) {
	t.Parallel()
	ag, sess := newWindowTestAgent(t)
	sess.AddUser("refactor the parser")
	for i := 0; i < 40; i++ {
		sess.AddAssistantToolCalls("", []ollama.MessageToolCall{{Function: ollama.MessageToolCallFunction{Name: "Bash", Arguments: map[string]any{}}}})
		sess.AddToolResult("Bash", strings.Repeat("y", 2000))
	}

	msgs, _ := ag.buildOllamaMessages(context.Background(), "SYSTEM")

	content, ok := firstUserContent(msgs)
	if !ok {
		t.Fatalf("request carries no user message: %d messages, roles=%v", len(msgs), rolesOf(msgs))
	}
	if content != "refactor the parser" {
		t.Fatalf("user anchor = %q, want the original request", content)
	}
}

func rolesOf(msgs []ollama.Message) []string {
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.Role)
	}
	return out
}
