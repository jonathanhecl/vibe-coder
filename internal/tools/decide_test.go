package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/decision"
)

type stubDecider struct {
	resp *decision.Response
	err  error
}

func (s *stubDecider) Decide(context.Context, decision.Request) (*decision.Response, error) {
	return s.resp, s.err
}
func (s *stubDecider) DecideBool(context.Context, string, string) (bool, error) { return false, nil }
func (s *stubDecider) DecideChoice(context.Context, string, string, ...string) (string, int, error) {
	return "", -1, nil
}
func (s *stubDecider) IsCommandDangerous(context.Context, string) (bool, error)      { return false, nil }
func (s *stubDecider) IsCommandNetworkAccess(context.Context, string) (bool, error)  { return false, nil }
func (s *stubDecider) DisambiguatePath(context.Context, string, []string) (string, bool, error) {
	return "", false, nil
}
func (s *stubDecider) CheckGoalCompletion(context.Context, string, string) (bool, error) {
	return false, nil
}
func (s *stubDecider) ClassifyFailure(context.Context, string) (string, error) { return "", nil }
func (s *stubDecider) ClassifyCommit(context.Context, string) (string, error)  { return "", nil }
func (s *stubDecider) CheckThinkingLoop(context.Context, string) (bool, error) { return false, nil }
func (s *stubDecider) Enabled() bool                                           { return true }
func (s *stubDecider) Model() string                                           { return "stub" }

func TestDecideTool(t *testing.T) {
	tool := NewDecideTool(&stubDecider{resp: &decision.Response{Choice: "beta", Option: "beta", Index: 1}})
	if tool.Name() != "Decide" {
		t.Fatalf("expected tool name Decide, got %q", tool.Name())
	}
	res := tool.Execute(context.Background(), map[string]any{
		"state": "ctx", "question": "pick", "options": []any{"alpha", "beta"},
	})
	if res.IsError || !strings.Contains(res.Output, "beta") {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestDecideToolValidation(t *testing.T) {
	tool := NewDecideTool(&stubDecider{})
	for name, params := range map[string]map[string]any{
		"missing state":    {"question": "q", "options": []any{"a", "b"}},
		"missing question": {"state": "s", "options": []any{"a", "b"}},
		"too few options":  {"state": "s", "question": "q", "options": []any{"a"}},
		"non-string opt":   {"state": "s", "question": "q", "options": []any{"a", 2}},
	} {
		if res := tool.Execute(context.Background(), params); !res.IsError {
			t.Fatalf("%s: expected error", name)
		}
	}
}
