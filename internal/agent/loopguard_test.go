package agent

import (
	"context"
	"fmt"
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
	if !strings.Contains(err.Error(), "press Enter to continue") {
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
	ag.inspection.streak = inspectionLimit

	if err := ag.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if ag.noProgress.streak != 0 {
		t.Fatalf("the new turn inherited a streak of %d", ag.noProgress.streak)
	}
	if ag.inspection.streak != 0 {
		t.Fatalf("the new turn inherited an inspection streak of %d", ag.inspection.streak)
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

// inspectionLoopClient varies both the command and its output. The original
// identical-call guard cannot see this stall; only the inspection streak can.
type inspectionLoopClient struct {
	coverageClient
}

func (c *inspectionLoopClient) Chat(_ context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	call := c.chatCalls.Add(1)
	content := fmt.Sprintf(`<invoke name="Bash">{"command":"printf 'check %d'"}</invoke>`, call)
	for _, m := range req.Messages {
		if strings.Contains(m.Content, "Stop inspecting") {
			content = "done: acted on the known evidence"
			break
		}
	}
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: content, Done: true}
	close(ch)
	return ch, nil
}

func TestRunRecoversFromDifferentReadOnlyInspections(t *testing.T) {
	client := &inspectionLoopClient{}
	ag := newCoverageAgent(t, client, tui.DecisionAllowOnce, true)

	if err := ag.Run(context.Background(), "produce the artifact"); err != nil {
		t.Fatalf("Run returned %v, want the turn to recover and finish", err)
	}
	if got, want := client.chatCalls.Load(), int32(inspectionLimit+1); got != want {
		t.Fatalf("chat calls = %d, want %d (inspection limit plus recovery)", got, want)
	}
	if !diagnosticsTranscriptHas(ag, "Stop inspecting") {
		t.Error("the model was not told to stop re-confirming known state")
	}
	if !diagnosticsTranscriptHas(ag, "Verify changes after making them") {
		t.Error("the recovery did not place verification after the state change")
	}
	if !diagnosticsTranscriptHas(ag, "done: acted on the known evidence") {
		t.Error("the recovered answer is missing from the transcript")
	}
}

func TestInspectionGuardResetsOnlyOnSuccessfulConcreteAction(t *testing.T) {
	g := new(inspectionGuard)
	for i := 0; i < inspectionLimit-1; i++ {
		if _, loop := g.observe(true, true); loop {
			t.Fatalf("inspection guard tripped early at %d", i+1)
		}
	}
	if streak, loop := g.observe(false, false); loop || streak != inspectionLimit-1 {
		t.Fatalf("a failed action changed the streak to %d (loop=%t)", streak, loop)
	}
	if streak, loop := g.observe(false, true); loop || streak != 0 {
		t.Fatalf("a successful concrete action left streak %d (loop=%t)", streak, loop)
	}
}

func TestLocalInspectionClassification(t *testing.T) {
	ag := newCoverageAgent(t, &coverageClient{}, tui.DecisionAllowOnce, true)
	bash := ag.reg.Get("Bash")
	for _, command := range []string{"cat captions.txt", "wc -l captions.txt", "printf 'check 1'"} {
		if !ag.isLocalInspection(bash, map[string]any{"command": command}) {
			t.Errorf("%q should count as local inspection", command)
		}
	}
	for _, command := range []string{"go test ./...", "printf x > captions.txt"} {
		if ag.isLocalInspection(bash, map[string]any{"command": command}) {
			t.Errorf("%q should count as a concrete action, not inspection", command)
		}
	}
	if !ag.isLocalInspection(ag.reg.Get("Read"), map[string]any{"file_path": "captions.txt"}) {
		t.Fatal("Read should count as local inspection in execution mode")
	}
	ag.EnterPlanMode()
	if ag.isLocalInspection(ag.reg.Get("Read"), map[string]any{"file_path": "captions.txt"}) {
		t.Fatal("plan mode must not treat its intended reading as an execution stall")
	}
	ag.ExitPlanMode()
	ag.EnterReviewMode()
	if ag.isLocalInspection(ag.reg.Get("Read"), map[string]any{"file_path": "captions.txt"}) {
		t.Fatal("review mode must not treat its intended reading as an execution stall")
	}
}
