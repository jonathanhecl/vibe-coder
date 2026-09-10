package ollama

import (
	"context"
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
