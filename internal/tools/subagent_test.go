package tools

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

type sequenceSubClient struct {
	replies []string
	calls   int
}

func (c *sequenceSubClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (sequenceSubClient) Version(context.Context) (string, error)         { return "0.0.0", nil }
func (sequenceSubClient) ChatSync(context.Context, ollama.ChatRequest) (ollama.ChatResponse, error) {
	return ollama.ChatResponse{Content: "summary"}, nil
}
func (sequenceSubClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return nil
}

func (c *sequenceSubClient) Chat(_ context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	reply := "done"
	if c.calls < len(c.replies) {
		// Support reply templates referencing the last user observation.
		reply = c.replies[c.calls]
	}
	c.calls++
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: reply, Done: true}
	close(ch)
	return ch, nil
}

func TestSubAgentRunsReadToolInLoop(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "notes.txt")
	if err := os.WriteFile(target, []byte("secret findings\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	t.Chdir(tmp)

	cfg := &config.Config{Model: "test", ContextWindow: 4096, MaxTokens: 128, Temperature: 0.0}
	client := &sequenceSubClient{replies: []string{
		`<invoke name="Read">{"file_path":"` + filepath.ToSlash(target) + `"}</invoke>`,
		"Findings: secret findings",
	}}
	sub := NewSubAgentTool(cfg, client)
	out := sub.Execute(context.Background(), map[string]any{"prompt": "summarize notes.txt", "max_turns": 3})
	if out.IsError {
		t.Fatalf("subagent failed: %s", out.Output)
	}
	if !strings.Contains(out.Output, "secret findings") {
		t.Fatalf("expected delegated answer, got %q", out.Output)
	}
	if client.calls != 2 {
		t.Fatalf("expected 2 model turns (tool + answer), got %d", client.calls)
	}
}

func TestSubAgentRejectsDisallowedWriteTool(t *testing.T) {
	cfg := &config.Config{Model: "test", ContextWindow: 4096, MaxTokens: 128, Temperature: 0.0}
	client := &sequenceSubClient{replies: []string{
		`<invoke name="Write">{"file_path":"/tmp/x.txt","content":"hi"}</invoke>`,
		"I could not write (read-only), stopping.",
	}}
	sub := NewSubAgentTool(cfg, client)
	out := sub.Execute(context.Background(), map[string]any{"prompt": "write a file", "max_turns": 2})
	if out.IsError {
		t.Fatalf("subagent failed: %s", out.Output)
	}
	if !strings.Contains(out.Output, "read-only") {
		t.Fatalf("expected read-only stop, got %q", out.Output)
	}
}

func TestParseSubAgentInvokes(t *testing.T) {
	calls := parseSubAgentInvokes(
		`<invoke name="Glob">{"pattern":"*.go"}</invoke><invoke name="Read">{"file_path":"a"}</invoke>`, 3)
	if len(calls) != 2 || calls[0].name != "Glob" || calls[1].name != "Read" {
		t.Fatalf("unexpected calls: %#v", calls)
	}
}
