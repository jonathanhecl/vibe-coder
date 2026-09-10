package ollama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMergeToolCallsAccumulatesAndDedupes(t *testing.T) {
	t.Parallel()
	a := MessageToolCall{Function: MessageToolCallFunction{Name: "Read", Arguments: map[string]any{"file_path": "a.go"}}}
	b := MessageToolCall{Function: MessageToolCallFunction{Name: "Glob", Arguments: map[string]any{"pattern": "*.go"}}}
	got := MergeToolCalls(nil, []MessageToolCall{a})
	got = MergeToolCalls(got, []MessageToolCall{b, a})
	if len(got) != 2 || got[0].Function.Name != "Read" || got[1].Function.Name != "Glob" {
		t.Fatalf("unexpected accumulation: %#v", got)
	}
}

func TestChatAccumulatesToolCallsAcrossChunks(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"content":"","tool_calls":[{"function":{"name":"Read","arguments":{"file_path":"a.go"}}}]},"done":false}` + "\n"))
		_, _ = w.Write([]byte(`{"message":{"content":"","tool_calls":[{"function":{"name":"Glob","arguments":{"pattern":"*.go"}}}]},"done":true}` + "\n"))
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL)
	stream, err := client.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, calls, err := drainStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || calls[0].Function.Name != "Read" || calls[1].Function.Name != "Glob" {
		t.Fatalf("expected both streamed tool calls, got %#v", calls)
	}
}

func TestToolCallStringifiedArguments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		raw  string
		want map[string]any
	}{
		{"object form", `{"name":"Read","arguments":{"file_path":"output.mp4"}}`, map[string]any{"file_path": "output.mp4"}},
		{"stringified form", `{"name":"Read","arguments":"{\"file_path\":\"output.mp4\"}"}`, map[string]any{"file_path": "output.mp4"}},
		{"empty string", `{"name":"Read","arguments":""}`, nil},
		{"null", `{"name":"Read","arguments":null}`, nil},
		{"missing", `{"name":"Read"}`, nil},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var got MessageToolCallFunction
			if err := json.Unmarshal([]byte(tc.raw), &got); err != nil {
				t.Fatalf("unmarshal failed: %v", err)
			}
			if got.Name != "Read" {
				t.Fatalf("unexpected name: %q", got.Name)
			}
			if len(tc.want) == 0 && len(got.Arguments) != 0 {
				t.Fatalf("expected empty arguments, got %#v", got.Arguments)
			}
			for k, v := range tc.want {
				if got.Arguments[k] != v {
					t.Fatalf("unexpected argument %q: got %#v want %#v", k, got.Arguments[k], v)
				}
			}
		})
	}
}

func TestChatStringifiedToolCallsAcrossChunks(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(`{"message":{"content":"","tool_calls":[{"function":{"name":"Read","arguments":"{\"file_path\":\"output.mp4\"}"}}]},"done":true}` + "\n"))
	}))
	defer srv.Close()

	client := NewHTTP(srv.URL)
	stream, err := client.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Stream: true})
	if err != nil {
		t.Fatal(err)
	}
	_, _, calls, err := drainStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || calls[0].Function.Name != "Read" {
		t.Fatalf("expected one tool call, got %#v", calls)
	}
	if calls[0].Function.Arguments["file_path"] != "output.mp4" {
		t.Fatalf("unexpected arguments: %#v", calls[0].Function.Arguments)
	}
}
