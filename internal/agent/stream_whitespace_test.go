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

func TestStreamAssistantHidesPipeInvokeToolCalls(t *testing.T) {
	ui := &recordingUI{}
	cfg := &config.Config{Cwd: t.TempDir(), Model: "m"}
	ag := New(cfg, nil, tools.NewRegistry(), permissions.NewManager(cfg), session.New(cfg), ui)
	first := `<|tool_call>invoke name="WebFetch">{"url":"https://example.com/"}</invoke>`
	second := `<|tool_call>invoke name="Glob">{"pattern":"*.go"}</invoke>`
	fullReply := first + "\n" + second + "\nWorking on it."
	ch := make(chan ollama.Chunk, len(fullReply)+1)
	for _, r := range fullReply {
		ch <- ollama.Chunk{Delta: string(r)}
	}
	ch <- ollama.Chunk{Done: true}
	close(ch)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	full, native, err := ag.streamAssistantResponse(ctx, cancel, ch)
	if err != nil || len(native) != 0 || full != fullReply {
		t.Fatalf("unexpected stream response: %q, %v, %v", full, native, err)
	}
	if got := ui.streamed.String(); got != "Working on it." {
		t.Fatalf("visible stream = %q", got)
	}
	calls := parseXMLFallbackAll(full, MaxToolsPerReply)
	if len(calls) != 2 || calls[0].Name != "WebFetch" || calls[0].Params["url"] != "https://example.com/" || calls[1].Name != "Glob" || calls[1].Params["pattern"] != "*.go" {
		t.Fatalf("parsed calls = %#v", calls)
	}
}
