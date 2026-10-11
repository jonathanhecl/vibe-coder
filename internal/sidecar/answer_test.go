package sidecar

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func TestExtractAnswer(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		resp ollama.ChatResponse
		want string
	}{
		{"content wins", ollama.ChatResponse{Content: " A cat. ", Thinking: "hmm"}, "A cat."},
		{"inline think stripped", ollama.ChatResponse{Content: "<think>plan</think>\nA red car."}, "A red car."},
		{"unclosed think dropped, falls back to reasoning", ollama.ChatResponse{Content: "<think>still going", Thinking: "A blue sky."}, "A blue sky."},
		{"empty content uses reasoning", ollama.ChatResponse{Thinking: "<think>Two people on a beach.</think>"}, "Two people on a beach."},
		{"both empty", ollama.ChatResponse{}, ""},
	}
	for _, tc := range cases {
		if got := ExtractAnswer(tc.resp); got != tc.want {
			t.Errorf("%s: got %q want %q", tc.name, got, tc.want)
		}
	}
}
