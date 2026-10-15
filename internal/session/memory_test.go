package session

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

var errEmbed = errors.New("embed failed")

// fakeEmbedder returns deterministic two-dimensional vectors so ranking is
// predictable: text mentioning "alpha" is close to the query "alpha", and any
// other text is only weakly related.
type fakeEmbedder struct {
	calls int
}

func (f *fakeEmbedder) Embed(_ context.Context, model string, inputs []string) ([][]float64, error) {
	if model == "" {
		return nil, errEmbed
	}
	f.calls++
	out := make([][]float64, len(inputs))
	for i, in := range inputs {
		switch {
		case strings.Contains(in, "alpha"):
			out[i] = []float64{1, 0}
		case strings.Contains(in, "beta"):
			out[i] = []float64{0, 1}
		default:
			out[i] = []float64{0.5, 0.5}
		}
	}
	return out, nil
}

// embedClient satisfies ollama.Client (through the embedded sidecarClient) and
// ollama.Embedder (through Embed), which is what the session memory needs.
type embedClient struct {
	sidecarClient
	emb *fakeEmbedder
}

func (c embedClient) Embed(ctx context.Context, model string, inputs []string) ([][]float64, error) {
	return c.emb.Embed(ctx, model, inputs)
}

func embeddingConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Cwd:           t.TempDir(),
		SessionsDir:   t.TempDir(),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
		RoleModels: map[config.Role]config.RoleModel{
			config.RoleEmbedding: {Provider: "ollama", Model: "test-embed"},
		},
	}
}

// TestSessionMemoryDemoteAndRecall verifies the core tiered-memory contract:
// demoted text can be recalled by a related query.
func TestSessionMemoryDemoteAndRecall(t *testing.T) {
	t.Parallel()
	s := New(embeddingConfig(t))
	s.SetClient(embedClient{emb: &fakeEmbedder{}})

	s.ensureMemory().demote(context.Background(), []string{
		"user: we chose the alpha design",
		"user: unrelated chatter",
	})

	block := s.Recall(context.Background(), "alpha", 3)
	if !strings.Contains(block, "alpha design") {
		t.Fatalf("expected the alpha fragment in the recall block, got %q", block)
	}
	if !strings.Contains(block, "[Session memory]") {
		t.Fatalf("expected a session memory header, got %q", block)
	}
	if s.MemoryCount() != 2 {
		t.Fatalf("expected 2 remembered fragments, got %d", s.MemoryCount())
	}
}

// TestSessionMemoryDisabledWithoutEmbedder verifies that a client without an
// Embed method makes the store a silent no-op.
func TestSessionMemoryDisabledWithoutEmbedder(t *testing.T) {
	t.Parallel()
	s := New(embeddingConfig(t))
	s.SetClient(sidecarClient{})

	s.ensureMemory().demote(context.Background(), []string{"user: alpha"})
	if got := s.MemoryCount(); got != 0 {
		t.Fatalf("expected a disabled store to keep nothing, got %d", got)
	}
	if block := s.Recall(context.Background(), "alpha", 3); block != "" {
		t.Fatalf("expected an empty recall from a disabled store, got %q", block)
	}
}

// TestSessionMemoryDisabledWithoutModel verifies that an unbound embedding
// role disables the store even when the client can embed.
func TestSessionMemoryDisabledWithoutModel(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir(), ContextWindow: 32}
	s := New(cfg)
	s.SetClient(embedClient{emb: &fakeEmbedder{}})

	s.ensureMemory().demote(context.Background(), []string{"user: alpha"})
	if got := s.MemoryCount(); got != 0 {
		t.Fatalf("expected no fragments without an embedding model, got %d", got)
	}
}

// TestSessionMemoryTrimsToCap verifies the store keeps only the newest
// maxMemoryEntries fragments.
func TestSessionMemoryTrimsToCap(t *testing.T) {
	t.Parallel()
	s := New(embeddingConfig(t))
	s.SetClient(embedClient{emb: &fakeEmbedder{}})

	texts := make([]string, maxMemoryEntries+10)
	for i := range texts {
		texts[i] = "user: filler"
	}
	s.ensureMemory().demote(context.Background(), texts)

	if got := s.MemoryCount(); got != maxMemoryEntries {
		t.Fatalf("expected the store capped at %d, got %d", maxMemoryEntries, got)
	}
}

// TestCompactDemotesDiscardedHistory verifies the end-to-end tiered-memory
// path: compaction shrinks the live transcript and the discarded region stays
// recallable instead of being lost.
func TestCompactDemotesDiscardedHistory(t *testing.T) {
	t.Parallel()
	s := New(embeddingConfig(t))
	s.SetClient(embedClient{emb: &fakeEmbedder{}})

	s.AddUser("Build a REST API with Go and SQLite")
	for i := 0; i < 80; i++ {
		s.AddUser(strings.Repeat("z", 40) + " alpha marker")
	}
	before := s.MessageCount()

	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact failed: %v", err)
	}
	if s.MessageCount() >= before {
		t.Fatalf("expected the live transcript to shrink, before=%d after=%d", before, s.MessageCount())
	}
	if s.MemoryCount() == 0 {
		t.Fatal("expected the discarded history to be demoted into memory")
	}
	block := s.Recall(context.Background(), "alpha", 3)
	if !strings.Contains(block, "alpha marker") {
		t.Fatalf("expected to recall the discarded alpha fragment, got %q", block)
	}
}

func TestDemoteTextsSkipsSyntheticEnvelopes(t *testing.T) {
	t.Parallel()
	msgs := []Message{
		{Role: "user", Content: "[Earlier conversation summary]\nold summary"},
		{Role: "user", Content: "[RAG Context]\n// a.go:1\npackage a"},
		{Role: "user", Content: "[Session memory]\n- user: prior memory"},
		{Role: "user", Content: RuntimeReminderPrefix + "\nkeep working\n[/runtime_reminder]"},
		{Role: "assistant", Content: "[runtime] File updated: a.go."},
		{Role: "user", Content: "Implement feature alpha"},
		{Role: "assistant", Content: "Implemented alpha handler"},
	}
	texts := demoteTexts(msgs)
	if len(texts) != 2 {
		t.Fatalf("expected only 2 substantive fragments, got %d: %v", len(texts), texts)
	}
	for _, txt := range texts {
		if strings.Contains(txt, "Session memory") || strings.Contains(txt, "RAG Context") || strings.Contains(txt, "runtime") {
			t.Fatalf("synthetic block leaked into demoteTexts: %q", txt)
		}
	}
}

func TestFirstUserMessageSkipsSyntheticEnvelopes(t *testing.T) {
	t.Parallel()
	s := New(embeddingConfig(t))
	s.AddUser("[RAG Context]\nchunk")
	s.AddUser("[Session memory]\n- item")
	s.AddRuntimeReminder("keep going")
	s.AddUser("Actual user task")

	s.mu.RLock()
	first := s.firstUserMessageUnlocked()
	s.mu.RUnlock()
	if first == nil || first.Content != "Actual user task" {
		t.Fatalf("expected firstUserMessageUnlocked to return 'Actual user task', got %+v", first)
	}
}
