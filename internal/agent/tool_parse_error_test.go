package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// A truncated JSON argument string must fail before any permission prompt,
// plan-mode block or checkpoint. The tool's Execute must never run.
func TestExecuteToolFailsFastOnParseError(t *testing.T) {
	t.Parallel()
	ag := newCoverageAgent(t, &coverageClient{}, tui.DecisionDeny, false)
	ag.EnterPlanMode()
	write := tools.NewWriteTool()
	probe := &policyProbe{Tool: write, name: "Write"}
	params := map[string]any{
		tools.ParseErrorKey: "unexpected end of JSON input",
		tools.RawArgsKey:    `{"file_path":"/tmp/x","contents":"` + strings.Repeat("a", 30000),
	}

	result, outcome, err := ag.executeTool(context.Background(), probe, "Write", params, toolExecutionMode{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.ran() {
		t.Fatalf("expected toolRan outcome, got %v", outcome)
	}
	if probe.called {
		t.Fatal("tool Execute must not run when arguments failed to parse")
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	if !strings.Contains(result.Output, "not valid JSON") {
		t.Fatalf("expected parse error message, got %q", result.Output)
	}
	if !strings.Contains(result.Output, "raw arguments") {
		t.Fatalf("expected raw arguments head, got %q", result.Output)
	}
	if strings.Contains(result.Output, "attempted args") {
		t.Fatalf("raw arguments must not be duplicated as attempted args: %q", result.Output)
	}
	if result.HintsForModel == "" {
		t.Fatal("expected a hint to retry with smaller arguments")
	}
}

// A parse failure in plan mode reports the JSON error, not the plan-mode
// block, because the fail-fast runs before toolModeBlock.
func TestExecuteToolParseErrorBeatsPlanModeBlock(t *testing.T) {
	t.Parallel()
	ag := newCoverageAgent(t, &coverageClient{}, tui.DecisionDeny, false)
	ag.EnterPlanMode()
	write := tools.NewWriteTool()
	params := map[string]any{
		tools.ParseErrorKey: "unexpected end of JSON input",
		tools.RawArgsKey:    `{"file_path":"/tmp/x"`,
	}
	result, outcome, err := ag.executeTool(context.Background(), write, "Write", params, toolExecutionMode{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.ran() {
		t.Fatalf("parse error must surface as a ran outcome, got %v", outcome)
	}
	if strings.Contains(result.Output, "blocked in plan mode") {
		t.Fatalf("plan-mode block must not mask the parse error: %q", result.Output)
	}
	if !strings.Contains(result.Output, "not valid JSON") {
		t.Fatalf("expected parse error message, got %q", result.Output)
	}
}
