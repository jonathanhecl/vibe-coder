package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// alwaysReplyClient answers every request with the same visible text, which is
// what a model stuck on its own summary does.
type alwaysReplyClient struct {
	fakeClient
	reply string
	calls int
}

func (c *alwaysReplyClient) Chat(context.Context, ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	c.calls++
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: c.reply, Done: true}
	close(ch)
	return ch, nil
}

const (
	stallReplyOne = "El usuario decidió: clave propia y persistente, git ya inicializado."
	stallReplyTwo = "El usuario decidió clave propia y persistente en disco, git ya inicializado."
)

// A rephrase of the same answer is the same stall: word-set similarity, not
// byte equality, is what tells them apart from real progress.
func TestReplyRepeatGuardSeesRephrases(t *testing.T) {
	var g replyRepeatGuard
	if got := g.observe(stallReplyOne); got != 1 {
		t.Fatalf("first reply streak = %d, want 1", got)
	}
	if got := g.observe(stallReplyTwo); got != 2 {
		t.Fatalf("rephrased reply streak = %d, want 2", got)
	}
	if got := g.observe("Writing the API key to /etc/vibe/api.key now."); got != 1 {
		t.Fatalf("a different answer must reset the streak, got %d", got)
	}
	g.reset()
	if got := g.observe(stallReplyOne); got != 1 {
		t.Fatalf("after reset streak = %d, want 1", got)
	}
}

// The reminder has to change something in the request: the generic "continue
// with the remaining steps" text was already being ignored, so it now quotes
// the duplicate and names the item to execute.
func TestRepeatedReplyNoteNamesTheItemAndQuotesTheReply(t *testing.T) {
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "write the api key to disk", Status: tools.TodoStatusInProgress},
	})

	note := ag.repeatedReplyNote("El usuario decidió: clave propia y persistente.\n\nNothing else.")
	for _, want := range []string{"write the api key to disk", "clave propia y persistente", "TodoWrite"} {
		if !strings.Contains(note, want) {
			t.Fatalf("reminder %q is missing %q", note, want)
		}
	}
	if strings.Contains(note, "Nothing else") {
		t.Fatalf("reminder should quote the first line only: %q", note)
	}
}

// The turn used to ask up to 50 times while the checklist stood still. Two
// tolerated repeats, then the turn stops and says what is still open.
func TestRepeatedReplyStopsTheTurn(t *testing.T) {
	client := &alwaysReplyClient{reply: stallReplyOne}
	ag, _, reg := newTurnAgent(t, client)
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "write the api key to disk", Status: tools.TodoStatusInProgress},
		{ID: "2", Content: "restart the server", Status: tools.TodoStatusPending},
	})

	err := ag.Run(context.Background(), "genera la clave y arranca el server")
	if !IsRepeatedReplyErr(err) {
		t.Fatalf("expected the repeated-reply stop, got %v", err)
	}
	if client.calls != 3 {
		t.Fatalf("model was asked %d times, want 3", client.calls)
	}
	for _, want := range []string{"write the api key to disk", "continue"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("stop message %q is missing %q", err.Error(), want)
		}
	}
}

// Distinct answers must never be mistaken for a loop: the turn keeps asking.
func TestDistinctRepliesKeepTheTurnGoing(t *testing.T) {
	client := &sequenceClient{replies: []string{
		"First: reading the current config file to see the key layout.",
		"Second: writing the new provider entry with its endpoint.",
		"Third: restarting the service and checking the log tail.",
		"Fourth: reporting the verified state back to the user.",
	}}
	ag, _, reg := newTurnAgent(t, client)
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "configure the provider", Status: tools.TodoStatusInProgress},
	})

	err := ag.Run(context.Background(), "configure it")
	if IsRepeatedReplyErr(err) {
		t.Fatalf("distinct answers were flagged as a repeated reply: %v", err)
	}
	if client.calls < 4 {
		t.Fatalf("model was asked %d times, want at least 4 (it must not stop early)", client.calls)
	}
}
