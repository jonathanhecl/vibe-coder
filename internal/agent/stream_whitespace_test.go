package agent

import (
	"context"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// TestStreamAssistantPreservesWhitespaceDeltas guards against a regression
// where whitespace-only stream deltas were dropped: some models stream one
// character at a time, and silently discarding the " " and "\n" deltas collapsed
// the whole answer into a single run-on line with no spaces.
func TestStreamAssistantPreservesWhitespaceDeltas(t *testing.T) {
	ui := &recordingUI{}
	cfg := &config.Config{Cwd: t.TempDir(), Model: "m"}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), ui)

	want := "hola mundo\nlinea dos\n"
	// The trailing newline is intentionally dropped at stream end; interior
	// whitespace must be preserved.
	wantStreamed := "hola mundo\nlinea dos"
	ch := make(chan ollama.Chunk, len(want)+4)
	go func() {
		for _, r := range want {
			ch <- ollama.Chunk{Delta: string(r)}
		}
		ch <- ollama.Chunk{Done: true}
		close(ch)
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	full, _, err := ag.streamAssistantResponse(ctx, cancel, ch)
	if err != nil {
		t.Fatalf("streamAssistantResponse: %v", err)
	}
	if full != want {
		t.Fatalf("full reply mismatch:\n want %q\n  got %q", want, full)
	}
	if got := ui.streamed.String(); got != wantStreamed {
		t.Fatalf("streamed text lost whitespace:\n want %q\n  got %q", wantStreamed, got)
	}
}
