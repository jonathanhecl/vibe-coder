package tools

import (
	"context"
	"strings"
	"testing"
)

// recordingAsker replays scripted answers and keeps the requests it saw.
type recordingAsker struct {
	answers  []string
	requests []AskRequest
}

func (a *recordingAsker) Ask(req AskRequest) (string, error) {
	a.requests = append(a.requests, req)
	if len(a.answers) == 0 {
		return "", nil
	}
	line := a.answers[0]
	a.answers = a.answers[1:]
	return line, nil
}

func askerTestParams() map[string]any {
	return map[string]any{
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
}

func TestAskUserQuestionUsesAskerInsteadOfStdin(t *testing.T) {
	t.Parallel()

	asker := &recordingAsker{answers: []string{"2"}}
	// The tool's own reader must never be consumed once an asker is wired up.
	reader := strings.NewReader("1\n")
	var out strings.Builder
	tool := NewAskUserQuestionToolWithIO(reader, &out)
	tool.SetAsker(asker)

	res := tool.Execute(context.Background(), askerTestParams())
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, `"negativos":"b"`) {
		t.Fatalf("expected option id b, got %s", res.Output)
	}
	if reader.Len() != len("1\n") {
		t.Fatal("tool consumed stdin even though an asker was wired up")
	}
	if len(asker.requests) != 1 {
		t.Fatalf("expected 1 request, got %d", len(asker.requests))
	}
	got := asker.requests[0]
	if got.Title != "Negative prompts" || got.Prompt != "Choose policy" {
		t.Fatalf("unexpected request: %+v", got)
	}
	if len(got.Options) != 3 || got.Options[1].ID != "b" || got.Options[1].Label != "Incluir lista corta moderna" {
		t.Fatalf("unexpected options: %+v", got.Options)
	}
	if got.ErrNote != "" {
		t.Fatalf("first request must carry no note, got %q", got.ErrNote)
	}
}

func TestAskUserQuestionAskerReasksWithNoteOnInvalidAnswer(t *testing.T) {
	t.Parallel()

	asker := &recordingAsker{answers: []string{"99", "c"}}
	var out strings.Builder
	tool := NewAskUserQuestionToolWithIO(strings.NewReader(""), &out)
	tool.SetAsker(asker)

	res := tool.Execute(context.Background(), askerTestParams())
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, `"negativos":"c"`) {
		t.Fatalf("expected option id c, got %s", res.Output)
	}
	if len(asker.requests) != 2 {
		t.Fatalf("expected a retry, got %d requests", len(asker.requests))
	}
	if !strings.Contains(asker.requests[1].ErrNote, "Please enter a number (1-3)") {
		t.Fatalf("retry must explain the accepted range, got %q", asker.requests[1].ErrNote)
	}
}

func TestAskUserQuestionAskerDefaultsOnEmptyAnswer(t *testing.T) {
	t.Parallel()

	asker := &recordingAsker{answers: []string{""}}
	var out strings.Builder
	tool := NewAskUserQuestionToolWithIO(strings.NewReader(""), &out)
	tool.SetAsker(asker)

	res := tool.Execute(context.Background(), askerTestParams())
	if res.IsError {
		t.Fatalf("unexpected error: %s", res.Output)
	}
	if !strings.Contains(res.Output, `"negativos":"a"`) {
		t.Fatalf("empty answer must select the first option, got %s", res.Output)
	}
}

func TestRegistrySetAskerReachesAskUserQuestion(t *testing.T) {
	t.Parallel()

	reg := NewRegistry()
	reg.Register(NewAskUserQuestionTool())
	asker := &recordingAsker{answers: []string{"1"}}
	reg.SetAsker(asker)

	tool, ok := reg.Get("AskUserQuestion").(*AskUserQuestionTool)
	if !ok {
		t.Fatal("AskUserQuestion not registered")
	}
	if tool.asker == nil {
		t.Fatal("registry did not inject the asker")
	}
	reg.SetAsker(nil)
	if tool.asker == nil {
		t.Fatal("nil asker must not clear an existing one")
	}
}
