package jevstyle

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

type fakeOllamaClient struct {
	responseContent string
	responseErr     error
	lastReq         ollama.ChatRequest
}

func (f *fakeOllamaClient) Chat(ctx context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeOllamaClient) ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error) {
	f.lastReq = req
	if f.responseErr != nil {
		return ollama.ChatResponse{}, f.responseErr
	}
	return ollama.ChatResponse{
		Content: f.responseContent,
	}, nil
}

func (f *fakeOllamaClient) Tags(ctx context.Context) ([]ollama.Model, error) {
	return nil, nil
}

func (f *fakeOllamaClient) Version(ctx context.Context) (string, error) {
	return "0.0.0", nil
}

func (f *fakeOllamaClient) Pull(ctx context.Context, model string, progress func(ollama.PullEvent)) error {
	return nil
}

func TestFormatPrompt(t *testing.T) {
	t.Parallel()

	// 1. Valid 2 options
	prompt, err := FormatPrompt("The build passed.", "Is it green?", []string{"Yes", "No"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := "You are a decision function. Read the state, then answer the question by choosing exactly one option.\n\n" +
		"[State]\nThe build passed.\n\n" +
		"[Question]\nIs it green?\n\n" +
		"[Options]\nA. Yes\nB. No\n\nAnswer:"

	if prompt != expected {
		t.Fatalf("prompt mismatch:\ngot:\n%s\nwant:\n%s", prompt, expected)
	}

	// 2. Too few options
	_, err = FormatPrompt("state", "question", []string{"OnlyOne"})
	if err == nil {
		t.Fatal("expected error for < 2 options")
	}

	// 3. Too many options
	tooMany := make([]string, 27)
	for i := range tooMany {
		tooMany[i] = "opt"
	}
	_, err = FormatPrompt("state", "question", tooMany)
	if err == nil {
		t.Fatal("expected error for > 26 options")
	}
}

func TestParseChoice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input       string
		numOptions  int
		wantLetter  string
		wantIndex   int
		wantErrText string
	}{
		{input: " A", numOptions: 2, wantLetter: "A", wantIndex: 0},
		{input: "B", numOptions: 2, wantLetter: "B", wantIndex: 1},
		{input: "c", numOptions: 3, wantLetter: "C", wantIndex: 2},
		{input: "  B. No", numOptions: 2, wantLetter: "B", wantIndex: 1},
		{input: "C.", numOptions: 3, wantLetter: "C", wantIndex: 2},
		{input: "C", numOptions: 2, wantErrText: "out of range"},
		{input: "", numOptions: 2, wantErrText: "empty response"},
		{input: "   ", numOptions: 2, wantErrText: "empty response"},
		{input: "123", numOptions: 2, wantErrText: "could not extract option letter"},
	}

	for _, tc := range cases {
		letter, idx, err := ParseChoice(tc.input, tc.numOptions)
		if tc.wantErrText != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErrText) {
				t.Fatalf("for input %q: expected error containing %q, got %v", tc.input, tc.wantErrText, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("for input %q: unexpected error %v", tc.input, err)
		}
		if letter != tc.wantLetter || idx != tc.wantIndex {
			t.Fatalf("for input %q: got letter=%s idx=%d, want letter=%s idx=%d", tc.input, letter, idx, tc.wantLetter, tc.wantIndex)
		}
	}
}

func TestClientDecide(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		JevstyleModel: "test-jev-model",
	}
	fake := &fakeOllamaClient{
		responseContent: " B",
	}

	client := New(cfg, fake, WithMaxParallel(1), WithTimeout(5*time.Second))
	if !client.Enabled() {
		t.Fatal("expected client to be enabled")
	}
	if client.Model() != "test-jev-model" {
		t.Fatalf("got model %q, want test-jev-model", client.Model())
	}

	// 1. Decide
	resp, err := client.Decide(context.Background(), DecisionRequest{
		State:    "User requested shutdown",
		Question: "Should we exit?",
		Options:  []string{"Keep going", "Exit now"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Choice != "B" || resp.Index != 1 || resp.Option != "Exit now" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if fake.lastReq.Model != "test-jev-model" {
		t.Fatalf("expected request model test-jev-model, got %s", fake.lastReq.Model)
	}
	if fake.lastReq.Options.Temperature != 0 {
		t.Fatalf("expected temperature 0, got %v", fake.lastReq.Options.Temperature)
	}

	// 2. DecideBool
	fake.responseContent = " A"
	isTrue, err := client.DecideBool(context.Background(), "Condition is met", "Is ready?")
	if err != nil {
		t.Fatalf("unexpected DecideBool error: %v", err)
	}
	if !isTrue {
		t.Fatal("expected DecideBool true for choice A")
	}

	fake.responseContent = " B"
	isFalse, err := client.DecideBool(context.Background(), "Condition failed", "Is ready?")
	if err != nil {
		t.Fatalf("unexpected DecideBool error: %v", err)
	}
	if isFalse {
		t.Fatal("expected DecideBool false for choice B")
	}

	// 3. DecideChoice
	fake.responseContent = " C"
	opt, idx, err := client.DecideChoice(context.Background(), "Task is Go", "Language?", "Python", "Rust", "Go")
	if err != nil {
		t.Fatalf("unexpected DecideChoice error: %v", err)
	}
	if opt != "Go" || idx != 2 {
		t.Fatalf("got opt=%s idx=%d, want Go, 2", opt, idx)
	}

	// 4. Disabled client
	disabledCfg := &config.Config{JevstyleModel: ""}
	disabledClient := New(disabledCfg, fake)
	if disabledClient.Enabled() {
		t.Fatal("expected client to be disabled")
	}
	_, err = disabledClient.Decide(context.Background(), DecisionRequest{
		State:    "s",
		Question: "q",
		Options:  []string{"A", "B"},
	})
	if err == nil {
		t.Fatal("expected error on disabled client")
	}

	// 5. IsCommandDangerous
	fake.responseContent = " A"
	dangerous, err := client.IsCommandDangerous(context.Background(), "rm -rf .git")
	if err != nil || !dangerous {
		t.Fatalf("expected dangerous true, got %t (err: %v)", dangerous, err)
	}

	fake.responseContent = " B"
	dangerous, err = client.IsCommandDangerous(context.Background(), "git status")
	if err != nil || dangerous {
		t.Fatalf("expected dangerous false, got %t (err: %v)", dangerous, err)
	}

	// 6. DisambiguatePath
	fake.responseContent = " B"
	chosen, ok, err := client.DisambiguatePath(context.Background(), "flags.go", []string{"pkg/flags.go", "internal/config/flags.go"})
	if err != nil || !ok || chosen != "internal/config/flags.go" {
		t.Fatalf("unexpected disambiguate result: chosen=%q ok=%t err=%v", chosen, ok, err)
	}

	// 7. CheckGoalCompletion
	fake.responseContent = " A"
	completed, err := client.CheckGoalCompletion(context.Background(), "pass tests", "all tests passed")
	if err != nil || !completed {
		t.Fatalf("expected completed true, got %t (err: %v)", completed, err)
	}

	// 8. ClassifyFailure
	fake.responseContent = " A"
	cat, err := client.ClassifyFailure(context.Background(), "syntax error on line 12")
	if err != nil || !strings.Contains(cat, "Syntax") {
		t.Fatalf("expected syntax category, got %q (err: %v)", cat, err)
	}

	// 9. ClassifyCommit
	fake.responseContent = " D"
	commitType, err := client.ClassifyCommit(context.Background(), "+func TestX(t *testing.T)")
	if err != nil || commitType != "test" {
		t.Fatalf("expected commit type 'test', got %q (err: %v)", commitType, err)
	}
}
