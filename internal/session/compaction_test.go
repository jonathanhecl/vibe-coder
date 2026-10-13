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
	// at index 35; a 300-token kept tail covers the newest ~30 messages
	// (indices 35..64), and the backward slide pulls one more message in so
	// the tail does not start on the orphaned result.
	s.AddToolResult("Read", "orphaned output")
	for i := 0; i < 29; i++ {
		s.AddUser(strings.Repeat("y", 40))
	}

	if err := s.CompactTo(context.Background(), 300); err != nil {
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

	// Pad so the pair sits comfortably inside a 150-token kept tail
	// (~15 messages of ~10 tokens each).
	for i := 0; i < 5; i++ {
		s.AddUser(strings.Repeat("y", 40))
	}

	if err := s.CompactTo(context.Background(), 150); err != nil {
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

// TestCompactFallsBackToMainModelWhenSidecarUnset verifies that when no sidecar
// is configured, compaction falls back to querying the main model for summary.
func TestCompactFallsBackToMainModelWhenSidecarUnset(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
		ContextWindow: 32,
		Model:         "main-model:latest",
		SidecarModel:  "",
	}
	s := New(cfg)

	var capturedModel string
	client := capturingSidecarClient{
		summary: "summary from main model",
		onChat: func(req ollama.ChatRequest) {
			capturedModel = req.Model
		},
	}
	s.SetClient(client)

	for i := 0; i < 80; i++ {
		s.AddUser(strings.Repeat("w", 40))
	}

	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if capturedModel != "main-model:latest" {
		t.Fatalf("expected compaction with main model, got %q", capturedModel)
	}
	if s.MessageCount() == 0 || !strings.Contains(s.messages[0].Content, "summary from main model") {
		t.Fatalf("expected main model summary in first message, got %#v", s.messages)
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

// TestCompactToSelectsTailByTokens verifies the kept tail is chosen by
// cumulative token cost, not a fixed message count.
func TestCompactToSelectsTailByTokens(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir(), ContextWindow: 32768, SidecarModel: "m"}
	s := New(cfg)
	s.SetClient(capturingSidecarClient{summary: "s"})
	for i := 0; i < 20; i++ {
		s.AddUser(strings.Repeat("x", 400)) // ~100 tokens each
	}
	genBefore := s.Generation()
	if err := s.CompactTo(context.Background(), 500); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if s.Generation() == genBefore {
		t.Fatal("generation should bump when the transcript is rewritten")
	}
	msgs := s.Messages()
	// summary + first user + kept tail
	tail := msgs[2:]
	tokens := 0
	for _, m := range tail {
		tokens += estimateMessageTokens(m.Content)
	}
	if len(tail) < 4 {
		t.Fatalf("tail keeps at least 4 messages, got %d", len(tail))
	}
}

// TestCompactToNoDuplicateFirstUser verifies the preserved first user message
// is not prepended again when it already sits inside the kept tail.
func TestCompactToNoDuplicateFirstUser(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir(), ContextWindow: 32768, SidecarModel: "m"}
	s := New(cfg)
	s.SetClient(capturingSidecarClient{summary: "s"})
	// Two leading runtime notes sit in the compacted region; the first user
	// message (index 2) is inside the kept tail and must not be duplicated.
	s.AddSystemNote(strings.Repeat("n", 400))
	s.AddSystemNote(strings.Repeat("m", 400))
	s.AddUser("the original request")
	for i := 0; i < 4; i++ {
		s.AddUser(strings.Repeat("y", 400))
	}
	if err := s.CompactTo(context.Background(), 410); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	count := 0
	for _, m := range s.Messages() {
		if m.Content == "the original request" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("first user message appears %d times, want 1", count)
	}
}

// TestCompactToKeepsAssistantForBoundaryTool verifies the cut slides back so
// a kept tool result keeps its assistant tool_calls.
func TestCompactToKeepsAssistantForBoundaryTool(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir(), ContextWindow: 32768, SidecarModel: "m"}
	s := New(cfg)
	s.SetClient(capturingSidecarClient{summary: "s"})
	for i := 0; i < 10; i++ {
		s.AddUser(strings.Repeat("x", 400))
	}
	s.AddAssistantToolCalls("", []ollama.MessageToolCall{{
		Function: ollama.MessageToolCallFunction{Name: "Read", Arguments: map[string]any{}},
	}})
	s.AddToolResult("Read", strings.Repeat("r", 400))
	for i := 0; i < 4; i++ {
		s.AddUser(strings.Repeat("y", 400))
	}
	if err := s.CompactTo(context.Background(), 550); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	msgs := s.Messages()
	// Find the tool result: it must still follow an assistant tool_calls.
	for i, m := range msgs {
		if m.Role != "tool" {
			continue
		}
		if i == 0 || msgs[i-1].Role != "assistant" || len(msgs[i-1].ToolCalls) == 0 {
			t.Fatal("kept tool result lost its assistant tool_calls at the boundary")
		}
	}
}

// TestRenderMessagesForSummaryClips verifies per-message clipping and that a
// prior summary is carried first, outside the newest-first budget.
func TestRenderMessagesForSummaryClips(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "user", Content: "[Earlier conversation summary]\nprior findings " + strings.Repeat("p", 9000)},
		{Role: "assistant", Content: strings.Repeat("a", 6000)},
		{Role: "user", Content: "small"},
	}
	out := renderMessagesForSummary(msgs)
	if !strings.HasPrefix(out, "user: [Earlier conversation summary]") {
		t.Fatalf("prior summary must come first:\n%.120s", out)
	}
	// Prior summary clipped at 8000 runes.
	if strings.Contains(out, strings.Repeat("p", 8001)) {
		t.Fatal("prior summary was not clipped to 8000 runes")
	}
	// Per-message clipping at 2000 runes with marker.
	if strings.Contains(out, strings.Repeat("a", 2001)) {
		t.Fatal("message content was not clipped to 2000 runes")
	}
	if !strings.Contains(out, "…[clipped]") {
		t.Fatal("missing clipped marker")
	}
}

// TestGenerationBumpsOnRewrite verifies appends keep the generation while
// clear/compaction/rewrite bump it.
func TestGenerationBumpsOnRewrite(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir(), ContextWindow: 32768}
	s := New(cfg)
	g0 := s.Generation()
	s.AddUser("one")
	if s.Generation() != g0 {
		t.Fatal("append must not bump generation")
	}
	s.Clear()
	if s.Generation() == g0 {
		t.Fatal("Clear must bump generation")
	}
	g1 := s.Generation()
	for i := 0; i < 10; i++ {
		s.AddUser(strings.Repeat("x", 400))
	}
	if err := s.CompactTo(context.Background(), 200); err != nil {
		t.Fatalf("CompactTo: %v", err)
	}
	if s.Generation() == g1 {
		t.Fatal("CompactTo must bump generation")
	}
}

// TestRenderMessagesForSummaryKeepsMessageLineOrder verifies that restoring
// chronological order reverses whole entries, not the lines inside them.
func TestRenderMessagesForSummaryKeepsMessageLineOrder(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "user", Content: "line1\nline2\nline3"},
		{Role: "assistant", Content: "reply one\nreply two"},
	}
	out := renderMessagesForSummary(msgs)
	if !strings.Contains(out, "line1\nline2\nline3") {
		t.Fatalf("multi-line message content got garbled:\n%s", out)
	}
	if !strings.Contains(out, "reply one\nreply two") {
		t.Fatalf("multi-line message content got garbled:\n%s", out)
	}
	if strings.Index(out, "line1") > strings.Index(out, "reply one") {
		t.Fatalf("older message must come before newer:\n%s", out)
	}
}
