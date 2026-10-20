package tools

import (
	"context"
	"fmt"
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

func TestAskUserQuestionSelectionModesAndFeedback(t *testing.T) {
	t.Parallel()

	params := map[string]any{
		"title": "Negative prompts",
		"questions": []any{
			map[string]any{
				"id":     "negativos",
				"prompt": "Choose policy",
				"options": []any{
					map[string]any{"id": "a", "label": "Omitir por default"},
					map[string]any{"id": "b", "label": "Incluir lista corta moderna"},
					map[string]any{"id": "c", "label": "Incluir lista clásica SD"},
				},
			},
		},
	}

	tests := []struct {
		name         string
		input        string
		wantID       string
		wantFeedback string
	}{
		{
			name:         "numeric selection 2",
			input:        "2\n",
			wantID:       "b",
			wantFeedback: "Selected: [2] Incluir lista corta moderna",
		},
		{
			name:         "empty input defaults to 1",
			input:        "\n",
			wantID:       "a",
			wantFeedback: "Selected: [1] Omitir por default",
		},
		{
			name:         "option id selection",
			input:        "c\n",
			wantID:       "c",
			wantFeedback: "Selected: [3] Incluir lista clásica SD",
		},
		{
			name:         "label prefix selection",
			input:        "omitir\n",
			wantID:       "a",
			wantFeedback: "Selected: [1] Omitir por default",
		},
		{
			name:         "invalid input then valid choice",
			input:        "99\n1\n",
			wantID:       "a",
			wantFeedback: "Selected: [1] Omitir por default",
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			in := strings.NewReader(tc.input)
			var out strings.Builder
			tool := NewAskUserQuestionToolWithIO(in, &out)

			res := tool.Execute(context.Background(), params)
			if res.IsError {
				t.Fatalf("unexpected error: %s", res.Output)
			}
			if !strings.Contains(res.Output, fmt.Sprintf(`"negativos":%q`, tc.wantID)) {
				t.Fatalf("output %s missing expected id %q", res.Output, tc.wantID)
			}

			rendered := out.String()
			if !strings.Contains(rendered, "Negative prompts") {
				t.Fatalf("rendered output missing title: %s", rendered)
			}
			if !strings.Contains(rendered, tc.wantFeedback) {
				t.Fatalf("rendered output missing feedback %q: %s", tc.wantFeedback, rendered)
			}
		})
	}
}

func TestAskUserQuestionTopLevelPromptAndOptions(t *testing.T) {
	t.Parallel()

	in := strings.NewReader("1\n")
	var out strings.Builder
	tool := NewAskUserQuestionToolWithIO(in, &out)

	res := tool.Execute(context.Background(), map[string]any{
		"prompt":  "Regla de duración para el sistema",
		"options": []any{"Omitir por default", "Incluir lista corta moderna"},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, `"q1":"1"`) {
		t.Fatalf("unexpected output: %s", res.Output)
	}
	rendered := out.String()
	if !strings.Contains(rendered, "Regla de duración para el sistema") {
		t.Fatalf("rendered output missing prompt: %s", rendered)
	}
	if !strings.Contains(rendered, "Selected: [1] Omitir por default") {
		t.Fatalf("rendered output missing feedback: %s", rendered)
	}
}
