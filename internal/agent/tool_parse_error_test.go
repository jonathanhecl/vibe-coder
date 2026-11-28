package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
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

type repairSyncClient struct {
	coverageClient
	lastReq   ollama.ChatRequest
	syncCalls int
	replyJSON string
}

func (c *repairSyncClient) ChatSync(_ context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	c.syncCalls++
	c.lastReq = req
	return ollama.ChatResponse{Content: c.replyJSON}, nil
}

func TestExecuteToolConstrainedRepairSucceeds(t *testing.T) {
	t.Parallel()
	target := filepath.ToSlash(filepath.Join(t.TempDir(), "repaired.txt"))
	client := &repairSyncClient{
		replyJSON: `{"file_path":"` + target + `","contents":"repaired content"}`,
	}
	ag := newCoverageAgent(t, client, tui.DecisionAllowOnce, true)
	write := tools.NewWriteTool()
	params := map[string]any{
		tools.ParseErrorKey: "invalid character 'c' after object key:value pair",
		tools.RawArgsKey:    `{"file_path":"` + target + `" "contents":"repaired content"}`,
	}

	result, outcome, err := ag.executeTool(context.Background(), write, "Write", params, toolExecutionMode{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !outcome.ran() || result.IsError {
		t.Fatalf("expected repaired Write to succeed, outcome=%v isErr=%t out=%q", outcome, result.IsError, result.Output)
	}
	if client.syncCalls != 1 {
		t.Fatalf("expected 1 ChatSync repair call, got %d", client.syncCalls)
	}
	if client.lastReq.Format == nil || len(client.lastReq.Format.Schema) == 0 {
		t.Fatalf("expected ChatSync to carry JSON schema Format, got %+v", client.lastReq.Format)
	}
	if client.lastReq.Think == nil || client.lastReq.Think.Enabled {
		t.Fatalf("expected repair request to disable thinking, got %+v", client.lastReq.Think)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "repaired content" {
		t.Fatalf("unexpected written file: data=%q err=%v", data, err)
	}
}

func TestExecuteToolConstrainedRepairRejectsMissingRequiredAndOversized(t *testing.T) {
	t.Parallel()
	client := &repairSyncClient{
		replyJSON: `{"contents":"missing file_path"}`,
	}
	ag := newCoverageAgent(t, client, tui.DecisionAllowOnce, true)
	write := tools.NewWriteTool()

	// Missing required key "file_path" must fall back to parse error instead of running Write.
	resMissing, _, err := ag.executeTool(context.Background(), write, "Write", map[string]any{
		tools.ParseErrorKey: "syntax error",
		tools.RawArgsKey:    `{"contents":"missing file_path"`,
	}, toolExecutionMode{})
	if err != nil || !resMissing.IsError || !strings.Contains(resMissing.Output, "not valid JSON") {
		t.Fatalf("expected fallback parse error when required key missing, got %+v err=%v", resMissing, err)
	}

	// Oversized payload (>8192 bytes) must skip ChatSync completely.
	prevCalls := client.syncCalls
	resBig, _, err := ag.executeTool(context.Background(), write, "Write", map[string]any{
		tools.ParseErrorKey: "unexpected end of JSON input",
		tools.RawArgsKey:    `{"file_path":"/tmp/x","contents":"` + strings.Repeat("z", 9000),
	}, toolExecutionMode{})
	if err != nil || !resBig.IsError || client.syncCalls != prevCalls {
		t.Fatalf("expected oversized payload to skip repair, calls=%d prev=%d err=%v", client.syncCalls, prevCalls, err)
	}
}

// A reply the provider cut at its output limit has the same parse failure as a
// malformed call, so the message must name the cause and the fix that works.
func TestExecuteToolParseErrorNamesOutputLimitTruncation(t *testing.T) {
	t.Parallel()
	ag := newCoverageAgent(t, &coverageClient{}, tui.DecisionDeny, false)
	ag.cfg.MaxTokens = 8192
	ag.lastStop = turnStop{Reason: ollama.StopReasonLength}
	params := map[string]any{
		tools.ParseErrorKey: "unexpected end of JSON input",
		tools.RawArgsKey:    `{"file_path":"/tmp/x","contents":"` + strings.Repeat("a", 30000),
	}

	result, _, err := ag.executeTool(context.Background(), tools.NewWriteTool(), "Write", params, toolExecutionMode{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error result")
	}
	for _, want := range []string{"max_tokens budget (8192)", "truncation, not a malformed call"} {
		if !strings.Contains(result.Output, want) {
			t.Fatalf("output missing %q: %q", want, result.Output)
		}
	}
	for _, want := range []string{"truncated at the output limit", "append=true"} {
		if !strings.Contains(result.HintsForModel, want) {
			t.Fatalf("hint missing %q: %q", want, result.HintsForModel)
		}
	}
}

// An interrupted stream is reported as a cut connection, and with no explicit
// cap the note does not invent a max_tokens number.
func TestExecuteToolParseErrorNamesInterruptedStream(t *testing.T) {
	t.Parallel()
	ag := newCoverageAgent(t, &coverageClient{}, tui.DecisionDeny, false)
	ag.cfg.MaxTokens = 0
	ag.lastStop = turnStop{Interrupted: true}
	params := map[string]any{
		tools.ParseErrorKey: "unexpected end of JSON input",
		tools.RawArgsKey:    `{"file_path":"/tmp/x","contents":"` + strings.Repeat("a", 30000),
	}

	result, _, err := ag.executeTool(context.Background(), tools.NewWriteTool(), "Write", params, toolExecutionMode{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(result.Output, "response stream ended before the provider finished") {
		t.Fatalf("expected an interrupted-stream note, got %q", result.Output)
	}
	if strings.Contains(result.Output, "max_tokens") {
		t.Fatalf("an interrupted stream must not be blamed on max_tokens: %q", result.Output)
	}
	if !strings.Contains(result.HintsForModel, "interrupted response stream") {
		t.Fatalf("expected an interrupted-stream hint, got %q", result.HintsForModel)
	}
}
