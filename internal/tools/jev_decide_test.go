package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/jevstyle"
)

type mockJevDecider struct {
	resp *jevstyle.DecisionResponse
	err  error
	on   bool
}

func (m *mockJevDecider) Decide(ctx context.Context, req jevstyle.DecisionRequest) (*jevstyle.DecisionResponse, error) {
	return m.resp, m.err
}
func (m *mockJevDecider) DecideBool(ctx context.Context, state, question string) (bool, error) {
	return false, nil
}
func (m *mockJevDecider) DecideChoice(ctx context.Context, state, question string, options ...string) (string, int, error) {
	return "", -1, nil
}
func (m *mockJevDecider) IsCommandDangerous(ctx context.Context, command string) (bool, error) {
	return false, nil
}
func (m *mockJevDecider) DisambiguatePath(ctx context.Context, hint string, candidates []string) (string, bool, error) {
	return "", false, nil
}
func (m *mockJevDecider) CheckGoalCompletion(ctx context.Context, goal, recentProgress string) (bool, error) {
	return false, nil
}
func (m *mockJevDecider) ClassifyFailure(ctx context.Context, errorLog string) (string, error) {
	return "", nil
}
func (m *mockJevDecider) ClassifyCommit(ctx context.Context, diffSummary string) (string, error) {
	return "", nil
}
func (m *mockJevDecider) Enabled() bool {
	return m.on
}
func (m *mockJevDecider) Model() string {
	return "mock-jev"
}

func TestJevDecideToolProperties(t *testing.T) {
	tool := NewJevDecideTool(nil)
	if tool.Name() != "JevDecide" {
		t.Fatalf("expected JevDecide, got %q", tool.Name())
	}
	if !strings.Contains(tool.Description(), "JEV Style") {
		t.Fatalf("description should mention JEV Style: %q", tool.Description())
	}
	schema := tool.Schema()
	if schema.Function.Name != "JevDecide" {
		t.Fatalf("schema function name mismatch: %q", schema.Function.Name)
	}
}

func TestJevDecideToolExecuteValidation(t *testing.T) {
	decider := &mockJevDecider{on: true}
	tool := NewJevDecideTool(decider)

	// Missing state
	res := tool.Execute(context.Background(), map[string]any{
		"question": "Which?",
		"options":  []string{"A", "B"},
	})
	if !res.IsError || !strings.Contains(res.Output, "state is required") {
		t.Fatalf("expected state required error, got %+v", res)
	}

	// Missing question
	res = tool.Execute(context.Background(), map[string]any{
		"state":   "Context info",
		"options": []string{"A", "B"},
	})
	if !res.IsError || !strings.Contains(res.Output, "question is required") {
		t.Fatalf("expected question required error, got %+v", res)
	}

	// Fewer than 2 options
	res = tool.Execute(context.Background(), map[string]any{
		"state":    "Context info",
		"question": "Which?",
		"options":  []string{"A"},
	})
	if !res.IsError || !strings.Contains(res.Output, "at least 2 items") {
		t.Fatalf("expected at least 2 items error, got %+v", res)
	}

	// Disabled decider
	decider.on = false
	res = tool.Execute(context.Background(), map[string]any{
		"state":    "Context info",
		"question": "Which?",
		"options":  []string{"A", "B"},
	})
	if !res.IsError || !strings.Contains(res.Output, "not configured or enabled") {
		t.Fatalf("expected disabled error, got %+v", res)
	}
}

func TestJevDecideToolExecuteSuccess(t *testing.T) {
	decider := &mockJevDecider{
		on: true,
		resp: &jevstyle.DecisionResponse{
			Choice: "B",
			Index:  1,
			Option: "Option Two",
		},
	}
	tool := NewJevDecideTool(decider)

	res := tool.Execute(context.Background(), map[string]any{
		"state":    "Context for decision",
		"question": "Which option is best?",
		"options":  []any{"Option One", "Option Two"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, "Decision: B. Option Two") {
		t.Fatalf("unexpected output: %q", res.Output)
	}

	// Failure branch
	decider.err = errors.New("timeout")
	res = tool.Execute(context.Background(), map[string]any{
		"state":    "Context for decision",
		"question": "Which option is best?",
		"options":  []any{"Option One", "Option Two"},
	})
	if !res.IsError || !strings.Contains(res.Output, "timeout") {
		t.Fatalf("expected timeout error result, got %+v", res)
	}
}
