package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

const repeatedNoopCall = `<invoke name="Bash">{"command":"true"}</invoke>`

// stuckClient repeats one identical no-op call until the transcript carries the
// recovery reminder, then answers. It proves the agent recovers inside the turn
// instead of ending the run the first time the guard trips.
type stuckClient struct {
	coverageClient
}

func (c *stuckClient) Chat(_ context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	c.chatCalls.Add(1)
	content := repeatedNoopCall
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "Stop repeating") {
			content = "done: changed approach"
			break
		}
	}
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: content, Done: true}
	close(ch)
	return ch, nil
}

// The guard exists to break a loop, not to end the turn: a model that repeats a
// call is told what it repeated and gets to try something else, so the user
// keeps getting an answer instead of landing back at the prompt.
func TestRunRecoversFromRepeatedToolCall(t *testing.T) {
	ag := newCoverageAgent(t, &stuckClient{}, tui.DecisionAllowOnce, true)

	if err := ag.Run(context.Background(), "loop please"); err != nil {
		t.Fatalf("Run returned %v, want the turn to recover and finish", err)
	}
	if !diagnosticsTranscriptHas(ag, "Stop repeating") {
		t.Error("the model was not told what it kept repeating")
	}
	if !diagnosticsTranscriptHas(ag, "done: changed approach") {
		t.Errorf("the recovered answer is missing from the transcript: %v", transcriptOf(ag))
	}
}

// When the reminders do not help, the stop must still be actionable: the caller
// reports it and the user can resume the same session.
func TestRunGivesUpWithResumeHintAfterRepeatedRetries(t *testing.T) {
	c := &coverageClient{reply: repeatedNoopCall}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)

	err := ag.Run(context.Background(), "loop please")
	if !IsNoProgressErr(err) {
		t.Fatalf("expected no-progress error, got %v", err)
	}
	if !strings.Contains(err.Error(), `send "continue" to resume`) {
		t.Errorf("the stop does not say how to resume: %v", err)
	}
	// One budget per retry plus the initial run: the guard cannot spin forever.
	if limit := (maxNoProgressRetries + 1) * noProgressLimit; int(c.chatCalls.Load()) > limit+2 {
		t.Fatalf("recovery kept retrying past its budget: %d chat calls", c.chatCalls.Load())
	}
}

// A streak belongs to the turn that spent it. Left in place, one stuck request
// made every later prompt die on its first identical call.
func TestRunResetsTheNoProgressStreakEachTurn(t *testing.T) {
	c := &coverageClient{reply: "answer"}
	ag := newCoverageAgent(t, c, tui.DecisionAllowOnce, true)
	ag.noProgress.call = "call from the previous turn"
	ag.noProgress.output = "output from the previous turn"
	ag.noProgress.streak = noProgressLimit

	if err := ag.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ag.noProgress.streak != 0 {
		t.Fatalf("the new turn inherited a streak of %d", ag.noProgress.streak)
	}
}

func TestNoProgressGuardResetsOnChangedOutput(t *testing.T) {
	g := new(noProgressGuard)
	call := "Read\x00{}"
	for i := 0; i < noProgressLimit-1; i++ {
		if _, loop := g.observe(call, "same"); loop {
			t.Fatalf("tripped early at %d", i)
		}
	}
	if _, loop := g.observe(call, "changed"); loop {
		t.Fatal("a changed output must reset the streak")
	}
}
