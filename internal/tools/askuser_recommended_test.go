package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

// The default taken by ESC and the timeout is option 1, so a model that flags
// another option as recommended must still end up with it there.
func TestParseAskQuestionsPromotesRecommendedOption(t *testing.T) {
	params := map[string]any{
		"prompt": "What next?",
		"options": []any{
			map[string]any{"label": "Option A"},
			map[string]any{"label": "Option B", "recommended": true},
			map[string]any{"label": "Option C"},
		},
	}
	qs, errMsg := parseAskQuestions(params)
	if errMsg != "" {
		t.Fatalf("parse error: %s", errMsg)
	}
	got := qs[0].options
	if got[0].label != "Option B" {
		t.Fatalf("recommended option should be first, got order %v", labelsOf(got))
	}
	if labelsOf(got) != "Option B,Option A,Option C" {
		t.Fatalf("other options should keep their relative order, got %v", labelsOf(got))
	}
	// Positional ids follow the new positions so the answer the model reads
	// back still names the option it sees.
	for i, o := range got {
		if want := string(rune('1' + i)); o.id != want {
			t.Fatalf("option %d id = %q, want %q", i, o.id, want)
		}
	}
}

func TestParseAskQuestionsKeepsOrderWithoutRecommendedFlag(t *testing.T) {
	params := map[string]any{
		"prompt":  "What next?",
		"options": []any{"Alpha", "Beta"},
	}
	qs, errMsg := parseAskQuestions(params)
	if errMsg != "" {
		t.Fatalf("parse error: %s", errMsg)
	}
	if labelsOf(qs[0].options) != "Alpha,Beta" {
		t.Fatalf("order must be untouched without a flag, got %v", labelsOf(qs[0].options))
	}
}

func TestParseAskQuestionsPromotesInsideMultiQuestion(t *testing.T) {
	params := map[string]any{
		"questions": []any{
			map[string]any{
				"id":      "q1",
				"prompt":  "First?",
				"options": []any{map[string]any{"label": "x"}, map[string]any{"label": "y", "best": true}},
			},
			map[string]any{
				"id":      "q2",
				"prompt":  "Second?",
				"options": []any{"m", "n"},
			},
		},
	}
	qs, errMsg := parseAskQuestions(params)
	if errMsg != "" {
		t.Fatalf("parse error: %s", errMsg)
	}
	if labelsOf(qs[0].options) != "y,x" {
		t.Fatalf("q1 should promote the flagged option, got %v", labelsOf(qs[0].options))
	}
	if labelsOf(qs[1].options) != "m,n" {
		t.Fatalf("q2 must keep its order, got %v", labelsOf(qs[1].options))
	}
}

// The answer handed back to the model must point at the option the user saw
// after promotion, not at the original position.
func TestAskRecommendedOptionAnswersWithPromotedID(t *testing.T) {
	out := &strings.Builder{}
	tool := NewAskUserQuestionToolWithIO(strings.NewReader("2\n"), out)
	res := tool.Execute(nil, map[string]any{
		"prompt": "What next?",
		"options": []any{
			map[string]any{"label": "Option A"},
			map[string]any{"label": "Option B", "recommended": true},
		},
	})
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	card := out.String()
	if strings.Index(card, "Option B") > strings.Index(card, "Option A") {
		t.Fatalf("card should list the recommended option first, got:\n%s", card)
	}
	var answers map[string]string
	if err := json.Unmarshal([]byte(res.Output), &answers); err != nil {
		t.Fatalf("bad json %q: %v", res.Output, err)
	}
	// Position 2 after promotion is Option A.
	if got := answers["q1"]; got != "2" {
		t.Fatalf("answer = %q, want position 2", got)
	}
}

func labelsOf(opts []askOption) string {
	parts := make([]string, len(opts))
	for i, o := range opts {
		parts[i] = o.label
	}
	return strings.Join(parts, ",")
}
