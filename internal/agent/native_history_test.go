package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// captureClient replays a canned native turn and records outgoing requests.
type captureClient struct {
	fakeClient
	mu       sync.Mutex
	requests []ollama.ChatRequest
	sent     bool
	readPath string
}

func (c *captureClient) Chat(_ context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	sent := c.sent
	c.sent = true
	c.mu.Unlock()
	ch := make(chan ollama.Chunk, 2)
	if !sent {
		ch <- ollama.Chunk{ToolCalls: []ollama.MessageToolCall{{Function: ollama.MessageToolCallFunction{
			Name: "Read", Arguments: map[string]any{"file_path": c.readPath},
		}}}}
		ch <- ollama.Chunk{ToolCalls: []ollama.MessageToolCall{{Function: ollama.MessageToolCallFunction{
			Name: "Grep", Arguments: map[string]any{"pattern": "hello", "path": c.readPath},
		}}}, Done: true}
	} else {
		ch <- ollama.Chunk{Delta: "all done", Done: true}
	}
	close(ch)
	return ch, nil
}

func TestNativeTurnReplaysStructuredHistory(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "a.txt")
	cfg := &config.Config{
		Model:         "test-model",
		ContextWindow: 32768,
		MaxTokens:     128,
		Temperature:   0.2,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	client := &captureClient{readPath: path}
	ag := New(cfg, client, reg, perm, sess, &fakeUI{})

	if err := ag.Run(context.Background(), "read and search the file"); err != nil {
		t.Fatalf("run agent: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 2 {
		t.Fatalf("expected two chat turns, got %d", len(client.requests))
	}
	var assistant *ollama.Message
	toolNames := map[string]bool{}
	for i := range client.requests[1].Messages {
		m := &client.requests[1].Messages[i]
		if m.Role == "assistant" && len(m.ToolCalls) > 0 {
			assistant = m
		}
		if m.Role == "tool" {
			if m.ToolName == "" {
				t.Fatal("tool result message missing tool_name")
			}
			toolNames[m.ToolName] = true
		}
	}
	if assistant == nil {
		t.Fatal("second request lacks assistant message with tool_calls")
	}
	if len(assistant.ToolCalls) != 2 {
		t.Fatalf("expected both accumulated calls in history, got %#v", assistant.ToolCalls)
	}
	if assistant.ToolCalls[0].Function.Arguments["file_path"] != path {
		t.Fatalf("call arguments lost: %#v", assistant.ToolCalls[0])
	}
	if !toolNames["Read"] || !toolNames["Grep"] {
		t.Fatalf("expected structured tool results for both calls, got %v", toolNames)
	}
}

func TestStructuredFieldsSurviveSessionPersistence(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: filepath.Join(tmp, "sessions")}
	s := session.New(cfg)
	s.AddAssistantToolCalls("", []ollama.MessageToolCall{{Function: ollama.MessageToolCallFunction{
		Name: "Read", Arguments: map[string]any{"file_path": "a.txt"},
	}}})
	s.AddToolResult("Read", "hello")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s2 := session.New(cfg)
	if err := s2.Load(s.ID()); err != nil {
		t.Fatal(err)
	}
	msgs := s2.MessagesReadOnly()
	if len(msgs) != 2 {
		t.Fatalf("expected two messages, got %d", len(msgs))
	}
	if msgs[0].ToolCalls[0].Function.Arguments["file_path"] != "a.txt" {
		t.Fatalf("tool_calls not persisted: %#v", msgs[0])
	}
	if msgs[1].Role != "tool" || msgs[1].ToolName != "Read" || msgs[1].Content != "hello" {
		t.Fatalf("tool result not persisted: %#v", msgs[1])
	}
}
