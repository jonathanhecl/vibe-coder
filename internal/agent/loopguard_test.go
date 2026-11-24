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
		t.Fatalf("Run returned %v, want the turn to recover after the soft nudge", err)
	}
	if got, want := client.chatCalls.Load(), int32(inspectionNudgeAt+1); got != want {
		t.Fatalf("chat calls = %d, want %d (soft nudge at %d plus recovery)", got, want, inspectionNudgeAt)
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

type stubbornInspectClient struct {
	coverageClient
}

func (c *stubbornInspectClient) Chat(_ context.Context, _ ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	call := c.chatCalls.Add(1)
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{
		Delta: fmt.Sprintf(`<invoke name="Bash">{"command":"printf 'check %d'"}</invoke>`, call),
		Done:  true,
	}
	close(ch)
	return ch, nil
}

func TestRunStopsWhenModelIgnoresInspectionNudge(t *testing.T) {
	client := &stubbornInspectClient{}
	ag := newCoverageAgent(t, client, tui.DecisionAllowOnce, true)

	err := ag.Run(context.Background(), "produce the artifact")
	if !IsInspectionLoopErr(err) {
		t.Fatalf("expected ErrInspectionLoop when model ignores nudge, got %v", err)
	}
	if got := client.chatCalls.Load(); got != inspectionLimit {
		t.Fatalf("chat calls = %d, want exactly %d (no infinite recovery resets)", got, inspectionLimit)
	}
}

func TestInspectionGuardResetsOnlyOnSuccessfulConcreteAction(t *testing.T) {
	g := new(inspectionGuard)
	nudged := false
	for i := 0; i < inspectionLimit-1; i++ {
		streak, nudge, stop := g.observe(true, false, true)
		if stop {
			t.Fatalf("inspection guard stopped early at %d", streak)
		}
		if nudge {
			if streak != inspectionNudgeAt {
				t.Fatalf("nudge fired at %d, want %d", streak, inspectionNudgeAt)
			}
			nudged = true
		}
	}
	if !nudged {
		t.Fatal("expected inspection nudge before hitting hard limit")
	}
	if streak, _, stop := g.observe(false, true, false); stop || streak != inspectionLimit-1 {
		t.Fatalf("a failed action changed the streak to %d (stop=%t)", streak, stop)
	}
	// Bookkeeping (TodoWrite, SessionNotes) must be neutral: it neither
	// advances the streak nor resets it, otherwise a read-note-read loop never
	// reaches the limit.
	if streak, _, stop := g.observe(false, false, true); stop || streak != inspectionLimit-1 {
		t.Fatalf("a bookkeeping call changed the streak to %d (stop=%t)", streak, stop)
	}
	if streak, _, stop := g.observe(false, true, true); stop || streak != 0 {
		t.Fatalf("a successful concrete action left streak %d (stop=%t)", streak, stop)
	}
}

func TestInspectionGuardIgnoresInterleavedBookkeeping(t *testing.T) {
	g := new(inspectionGuard)
	// Read, read, SessionNotes, TodoWrite, read... must still stop at
	// inspectionLimit even though neutral calls sit between them.
	reads := 0
	for i := 0; i < 40 && reads < inspectionLimit; i++ {
		switch i % 4 {
		case 2:
			// SessionNotes upsert: neutral.
			if _, _, stop := g.observe(false, false, true); stop {
				t.Fatal("neutral bookkeeping must never stop the guard")
			}
		case 3:
			// TodoWrite: neutral.
			if _, _, stop := g.observe(false, false, true); stop {
				t.Fatal("neutral bookkeeping must never stop the guard")
			}
		default:
			reads++
			if _, _, stop := g.observe(true, false, true); stop {
				if reads != inspectionLimit {
					t.Fatalf("stopped at %d reads, want %d", reads, inspectionLimit)
				}
			}
		}
	}
	if reads != inspectionLimit {
		t.Fatalf("neutral calls prevented the streak from reaching %d (got %d reads)", inspectionLimit, reads)
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
	// Every read counts now: external research and visual reads cannot be
	// used to launder an inspection-only run by alternating sources.
	for _, name := range []string{"WebFetch", "WebSearch", "DescribeImage", "BackgroundStatus"} {
		tool := ag.reg.Get(name)
		if tool == nil {
			continue
		}
		inspection, concrete := ag.classifyForProgress(tool, map[string]any{})
		if !inspection || concrete {
			t.Errorf("%s should count as inspection (got inspection=%t concrete=%t)", name, inspection, concrete)
		}
	}
	// Bookkeeping must stay neutral: TodoWrite and SessionNotes neither
	// advance nor reset the streak.
	for _, name := range []string{"TodoWrite", "SessionNotes"} {
		tool := ag.reg.Get(name)
		if tool == nil {
			continue
		}
		inspection, concrete := ag.classifyForProgress(tool, map[string]any{})
		if inspection || concrete {
			t.Errorf("%s should be neutral (got inspection=%t concrete=%t)", name, inspection, concrete)
		}
	}
	if inspection, concrete := ag.classifyForProgress(ag.reg.Get("Write"), map[string]any{"file_path": "a.txt"}); !concrete || inspection {
		t.Fatalf("Write should be concrete (got inspection=%t concrete=%t)", inspection, concrete)
	}
	if sub := ag.reg.Get("SubAgent"); sub != nil {
		if inspection, concrete := ag.classifyForProgress(sub, map[string]any{"prompt": "find foo"}); !inspection || concrete {
			t.Fatalf("read-only SubAgent must count as inspection (got inspection=%t concrete=%t)", inspection, concrete)
		}
		if inspection, concrete := ag.classifyForProgress(sub, map[string]any{"prompt": "fix foo", "allow_writes": true}); inspection || !concrete {
			t.Fatalf("write-enabled SubAgent must count as concrete (got inspection=%t concrete=%t)", inspection, concrete)
		}
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
