package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// emptyReplyClient answers every request with a done chunk carrying no text,
// no thinking and no tool call, which is what a wedged or over-budget model
// looks like on the wire.
type emptyReplyClient struct {
	fakeClient
	calls int
}

func (c *emptyReplyClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	c.calls++
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Done: true}
	close(ch)
	return ch, nil
}

// A turn that produces nothing used to end with `return nil`: the user saw no
// answer, no footer and no error, which reads as a crash. It must report the
// empty reply so the caller can offer a retry.
func TestRunReportsEmptyReplyInsteadOfEndingSilently(t *testing.T) {
	client := &emptyReplyClient{}
	ag := newCoverageAgent(t, client, tui.DecisionAllowOnce, false)
	err := ag.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("empty reply ended the run without reporting anything")
	}
	if !IsEmptyAssistantResponseErr(err) {
		t.Fatalf("expected an empty-response error, got %v", err)
	}
	if client.calls < 2 {
		t.Fatalf("expected retries before giving up, got %d call(s)", client.calls)
	}
	if !sessionHasReminder(ag, "no visible answer") {
		t.Fatal("retry was sent without telling the model what was missing")
	}
}

// Reasoning-only replies (thinking, no visible text, no tool call) took the
// same silent exit. They must also end with a reported empty response.
func TestRunReportsReasoningOnlyReplyInsteadOfEndingSilently(t *testing.T) {
	client := &sequenceClient{replies: []string{
		"<think>still weighing the options</think>",
		"<think>still weighing the options</think>",
		"<think>still weighing the options</think>",
	}}
	ag := newCoverageAgent(t, client, tui.DecisionAllowOnce, false)
	err := ag.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("reasoning-only reply ended the run without reporting anything")
	}
	if !IsEmptyAssistantResponseErr(err) {
		t.Fatalf("expected an empty-response error, got %v", err)
	}
	if !sessionHasReminder(ag, "no visible answer") {
		t.Fatal("retry was sent without telling the model what was missing")
	}
}

// An unknown tool envelope on its own leaves nothing readable on screen, so the
// turn must report it instead of ending successfully.
func TestRunReportsUnknownToolWithoutVisibleAnswer(t *testing.T) {
	client := &coverageClient{reply: `<invoke name="MissingTool">{"x":1}</invoke>`}
	ag := newCoverageAgent(t, client, tui.DecisionAllowOnce, false)
	err := ag.Run(context.Background(), "hello")
	if err == nil {
		t.Fatal("unknown tool envelope ended the run without a visible answer")
	}
	if !strings.Contains(err.Error(), "MissingTool") {
		t.Fatalf("expected the unknown tool to be named, got %v", err)
	}
}

// sessionHasReminder reports whether a runtime reminder mentioning needle was
// injected into the transcript.
func sessionHasReminder(ag *Agent, needle string) bool {
	for _, msg := range ag.sess.MessagesReadOnly() {
		if strings.Contains(msg.Content, "[runtime_reminder]") && strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}
