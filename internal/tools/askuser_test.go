package tools

import (
	"context"
	"strings"
	"testing"
)

func TestParseAskQuestionsRejectsStringArray(t *testing.T) {
	t.Parallel()
	_, err := parseAskQuestions(map[string]any{
		"questions": []any{"only strings", "bad"},
	})
	if err == "" {
		t.Fatal("expected error for string-only questions")
	}
	if !strings.Contains(err, "object") {
		t.Fatalf("expected hint about objects, got: %s", err)
	}
}

func TestParseAskQuestionsAcceptsStringOptions(t *testing.T) {
	t.Parallel()
	qs, err := parseAskQuestions(map[string]any{
		"questions": []any{
			map[string]any{
				"id":      "x",
				"prompt":  "Pick",
				"options": []any{"Alpha", "Beta"},
			},
		},
	})
	if err != "" {
		t.Fatal(err)
	}
	if len(qs) != 1 || len(qs[0].options) != 2 {
		t.Fatalf("got %+v", qs)
	}
	if qs[0].options[0].label != "Alpha" || qs[0].options[0].id != "1" {
		t.Fatalf("opt0: %+v", qs[0].options[0])
	}
}

func TestAskUserQuestionExecuteInvalidParams(t *testing.T) {
	t.Parallel()
	out := NewAskUserQuestionTool().Execute(context.Background(), map[string]any{
		"questions": []any{"bad"},
	})
	if !out.IsError {
		t.Fatal("expected error result")
	}
	if out.Output == "" {
		t.Fatal("expected error message")
	}
}
