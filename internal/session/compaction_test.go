package session

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// TestCompactPreservesFirstUserMessage verifies that the original user task
// is never summarized away — it must survive compaction verbatim so the agent
// never loses its goal.
func TestCompactPreservesFirstUserMessage(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
	}
	s := New(cfg)
	s.SetClient(sidecarClient{})

	s.AddUser("Build a REST API with Go and SQLite")
	for i := 0; i < 80; i++ {
		s.AddUser(strings.Repeat("z", 40))
	}

	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	found := false
	for _, m := range s.Messages() {
		if m.Role == "user" && m.Content == "Build a REST API with Go and SQLite" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("original user message was not preserved verbatim after compaction")
	}
}

// TestCompactConvertsOrphanedToolResults verifies that a role:"tool" message
// whose preceding assistant tool_calls were compacted away is converted to
// a user-role observation envelope, keeping the wire history valid.
func TestCompactConvertsOrphanedToolResults(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
	}
	s := New(cfg)
	s.SetClient(sidecarClient{})

	// Test the orphan case: a tool result in the kept tail whose
	// preceding assistant tool_calls was in the compacted portion.
	for i := 0; i < 35; i++ {
		s.AddUser(strings.Repeat("x", 40))
	}
	// Add a tool result WITHOUT a preceding assistant tool_calls. It lands
	// at index 35, which must be inside the kept tail (last 30).
	s.AddToolResult("Read", "orphaned output")
	// Pad with 29 more messages so the tool result at index 35 is in the
	// kept tail (indices 35..64 for 65 total, cut=35).
	for i := 0; i < 29; i++ {
		s.AddUser(strings.Repeat("y", 40))
	}

	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	msgs := s.Messages()
	hasOrphanedTool := false
	for _, m := range msgs {
		if m.Role == "tool" {
			hasOrphanedTool = true
		}
	}
	if hasOrphanedTool {
		t.Fatal("orphaned role:tool message should have been converted to user envelope")
	}

	// The converted message should contain the tool result content.
	foundConverted := false
	for _, m := range msgs {
		if m.Role == "user" && strings.Contains(m.Content, "orphaned output") && strings.Contains(m.Content, "[tool_result") {
			foundConverted = true
			break
		}
	}
	if !foundConverted {
		t.Fatal("orphaned tool result was not converted to a user observation envelope")
	}
}

// TestCompactKeepsValidToolResultPair verifies that a tool result with its
// preceding assistant tool_calls in the kept tail is NOT converted.
func TestCompactKeepsValidToolResultPair(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
	}
	s := New(cfg)
	s.SetClient(sidecarClient{})

	for i := 0; i < 35; i++ {
		s.AddUser(strings.Repeat("x", 40))
	}

	calls := []ollama.MessageToolCall{{
		Function: ollama.MessageToolCallFunction{
			Name:      "Read",
			Arguments: map[string]any{"path": "/tmp/test.go"},
		},
	}}
	s.AddAssistantToolCalls("", calls)
	s.AddToolResult("Read", "valid output")

	// Pad to ensure the pair is in the kept tail.
	for i := 0; i < 5; i++ {
		s.AddUser(strings.Repeat("y", 40))
	}

	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact failed: %v", err)
	}

	msgs := s.Messages()
	foundTool := false
	foundAssistantCalls := false
	for i, m := range msgs {
		if m.Role == "tool" && m.ToolName == "Read" && strings.Contains(m.Content, "valid output") {
			foundTool = true
			if i > 0 && msgs[i-1].Role == "assistant" && len(msgs[i-1].ToolCalls) > 0 {
				foundAssistantCalls = true
			}
		}
	}
	if !foundTool {
		t.Fatal("valid tool result was dropped from kept tail")
	}
	if !foundAssistantCalls {
		t.Fatal("valid tool result lost its preceding assistant tool_calls")
	}
}

// TestCompactThresholdSimplified verifies the simplified threshold logic
// does not compact when below the minimum message count.
func TestCompactThresholdDoesNotCompactBelowMinimum(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
	}
	s := New(cfg)
	s.SetClient(sidecarClient{})

	for i := 0; i < 29; i++ {
		s.AddUser(strings.Repeat("x", 40))
	}
	before := s.MessageCount()
	_ = s.Compact(context.Background(), false)
	if s.MessageCount() != before {
		t.Fatalf("compaction should not fire below %d messages: before=%d after=%d",
			compactionKeepTail, before, s.MessageCount())
	}
}

// TestCompactSummaryPromptPreservesTask verifies the sidecar receives the
// improved summary prompt that instructs it to preserve task details.
func TestCompactSummaryPromptPreservesTask(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
	}
	s := New(cfg)

	// Use a custom client that captures the system prompt.
	var capturedSystemPrompt string
	capturingClient := capturingSidecarClient{
		summary: "test summary",
		onChat:  func(req ollama.ChatRequest) { capturedSystemPrompt = req.Messages[0].Content },
	}
	s.SetClient(capturingClient)

	for i := 0; i < 80; i++ {
		s.AddUser(strings.Repeat("z", 40))
	}

	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if !strings.Contains(capturedSystemPrompt, "original task") ||
		!strings.Contains(capturedSystemPrompt, "TODO state") ||
		!strings.Contains(capturedSystemPrompt, "File paths") {
		t.Fatalf("summary prompt does not instruct preservation of key details: %q", capturedSystemPrompt)
	}
}

type capturingSidecarClient struct {
	summary string
	onChat  func(ollama.ChatRequest)
}

func (c capturingSidecarClient) Tags(context.Context) ([]ollama.Model, error) { return nil, nil }
func (c capturingSidecarClient) Version(context.Context) (string, error)      { return "0.0.0", nil }
func (c capturingSidecarClient) Pull(context.Context, string, func(ollama.PullEvent)) error {
	return nil
}
func (c capturingSidecarClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Done: true}
	close(ch)
	return ch, nil
}
func (c capturingSidecarClient) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	if c.onChat != nil {
		c.onChat(req)
	}
	return ollama.ChatResponse{Content: c.summary}, nil
}
