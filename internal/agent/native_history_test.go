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

type captureBatchClient struct {
	fakeClient
	mu        sync.Mutex
	requests  []ollama.ChatRequest
	sent      bool
	writePath string
	readPath  string
	denied    bool
}

func (c *captureBatchClient) Chat(_ context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	c.mu.Lock()
	c.requests = append(c.requests, req)
	sent := c.sent
	c.sent = true
	c.mu.Unlock()
	ch := make(chan ollama.Chunk, 3)
	if !sent {
		if c.denied {
			ch <- ollama.Chunk{ToolCalls: []ollama.MessageToolCall{
				{Function: ollama.MessageToolCallFunction{Name: "Bash", Arguments: map[string]any{"command": "echo secret"}}},
				{Function: ollama.MessageToolCallFunction{Name: "Read", Arguments: map[string]any{"file_path": c.readPath}}},
			}, Done: true}
		} else {
			ch <- ollama.Chunk{ToolCalls: []ollama.MessageToolCall{
				{Function: ollama.MessageToolCallFunction{Name: "Write", Arguments: map[string]any{"file_path": c.writePath, "contents": "hello world"}}},
				{Function: ollama.MessageToolCallFunction{Name: "Read", Arguments: map[string]any{"file_path": c.readPath}}},
			}, Done: true}
		}
	} else {
		ch <- ollama.Chunk{Delta: "finished", Done: true}
	}
	close(ch)
	return ch, nil
}

func TestNativeBatchWriteThenReadPreservesWireOrder(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "out.txt")
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
	client := &captureBatchClient{writePath: path, readPath: path}
	ag := New(cfg, client, reg, perm, sess, &fakeUI{})

	if err := ag.Run(context.Background(), "write then read"); err != nil {
		t.Fatalf("run agent: %v", err)
	}

	client.mu.Lock()
	defer client.mu.Unlock()
	if len(client.requests) != 2 {
		t.Fatalf("expected two chat turns, got %d", len(client.requests))
	}

	msgs := client.requests[1].Messages
	assistantIdx := -1
	for i, m := range msgs {
		if m.Role == "assistant" && len(m.ToolCalls) == 2 {
			assistantIdx = i
			break
		}
	}
	if assistantIdx < 0 {
		t.Fatal("expected assistant message with 2 tool_calls")
	}

	// Immediately following the assistant message must be the role="tool" messages for Write and Read
	if len(msgs) < assistantIdx+3 {
		t.Fatalf("not enough messages following assistant: %#v", msgs[assistantIdx:])
	}
	m1 := msgs[assistantIdx+1]
	m2 := msgs[assistantIdx+2]

	if m1.Role != "tool" || m1.ToolName != "Write" {
		t.Fatalf("expected first tool response to be Write tool, got role=%q tool=%q", m1.Role, m1.ToolName)
	}
	if m2.Role != "tool" || m2.ToolName != "Read" {
		t.Fatalf("expected second tool response to be Read tool, got role=%q tool=%q", m2.Role, m2.ToolName)
	}
}

func TestNativeBatchDeniedToolAnswersRemainingCalls(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "out.txt")
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
	// Deny Bash
	perm := permissions.NewManager(&config.Config{YesMode: false})
	perm.DenySession("Bash")
	client := &captureBatchClient{writePath: path, readPath: path, denied: true}
	ag := New(cfg, client, reg, perm, sess, &fakeUI{})

	// The Bash call is denied for the session; the Read that follows it in the
	// same batch still runs, so every declared call is answered.
	_ = ag.Run(context.Background(), "run bash then read")

	// Verify all tool calls declared by the assistant in the session were answered as role="tool"
	sessionMsgs := sess.Messages()
	var assistantMsg *session.Message
	for i := range sessionMsgs {
		if sessionMsgs[i].Role == "assistant" && len(sessionMsgs[i].ToolCalls) > 0 {
			assistantMsg = &sessionMsgs[i]
			break
		}
	}
	if assistantMsg == nil {
		t.Fatal("expected assistant message with tool calls in session")
	}

	toolResults := 0
	for _, m := range sessionMsgs {
		if m.Role == "tool" {
			toolResults++
		}
	}
	if toolResults != len(assistantMsg.ToolCalls) {
		t.Fatalf("expected %d tool results answering all declared calls, got %d", len(assistantMsg.ToolCalls), toolResults)
	}
}

