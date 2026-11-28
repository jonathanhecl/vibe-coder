package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// TestBuildChatRequestThinking covers the thinking level on the OpenAI wire:
// an explicit "off" travels as chat_template_kwargs.enable_thinking=false,
// the levels travel as reasoning_effort, and the default (on) leaves both out
// so a server that thinks by default keeps doing it.
func TestBuildChatRequestThinking(t *testing.T) {
	messages := []ollama.Message{{Role: "user", Content: "hi"}}
	off := false

	tests := []struct {
		name    string
		setting *ollama.ThinkSetting
		effort  string
		enable  *bool
	}{
		{"unset", nil, "", nil},
		{"on", ollama.ThinkOn(), "", nil},
		{"off", ollama.ThinkOff(), "", &off},
		{"low", ollama.ThinkLevel("low"), "low", nil},
		{"medium", ollama.ThinkLevel("medium"), "medium", nil},
		{"high", ollama.ThinkLevel("high"), "high", nil},
		{"max", ollama.ThinkLevel("max"), "high", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildChatRequest(ollama.ChatRequest{Model: "m", Messages: messages, Think: tt.setting})
			if err != nil {
				t.Fatalf("buildChatRequest failed: %v", err)
			}
			if got.ReasoningEffort != tt.effort {
				t.Errorf("reasoning_effort = %q, want %q", got.ReasoningEffort, tt.effort)
			}
			switch {
			case tt.enable == nil:
				if got.ChatTemplateKwargs != nil {
					t.Errorf("chat_template_kwargs = %+v, want omitted", got.ChatTemplateKwargs)
				}
			case got.ChatTemplateKwargs == nil || got.ChatTemplateKwargs.EnableThinking == nil:
				t.Errorf("chat_template_kwargs missing enable_thinking, want %t", *tt.enable)
			case *got.ChatTemplateKwargs.EnableThinking != *tt.enable:
				t.Errorf("enable_thinking = %t, want %t", *got.ChatTemplateKwargs.EnableThinking, *tt.enable)
			}

			payload, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("marshal failed: %v", err)
			}
			body := string(payload)
			if tt.enable != nil && !strings.Contains(body, `"chat_template_kwargs":{"enable_thinking":false}`) {
				t.Errorf("wire body does not carry the thinking switch turned off: %s", body)
			}
			if tt.enable == nil && strings.Contains(body, "chat_template_kwargs") {
				t.Errorf("wire body carries chat_template_kwargs when it should not: %s", body)
			}
		})
	}
}

func TestParseResponseChoicePreservesJSONError(t *testing.T) {
	truncated := `{"file_path": "a.txt", "contents": "abc`
	choice := chatChoice{Message: chatResponseMessage{
		ToolCalls: []chatToolCall{{Function: chatFunctionCall{Name: "Write", Arguments: truncated}}},
	}}
	got := parseResponseChoice(choice)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("got %d calls", len(got.ToolCalls))
	}
	args := got.ToolCalls[0].Function.Arguments
	if _, ok := args["__parse_error"]; !ok {
		t.Errorf("missing __parse_error in %v", args)
	}
	if raw, _ := args["__raw_arguments"].(string); raw != truncated {
		t.Errorf("raw not preserved: %q", raw)
	}
}

func TestBuildToolCallsPreservesStreamJSONError(t *testing.T) {
	acc := newStreamAccumulator()
	acc.accumulate(chatDelta{ToolCalls: []chatToolCall{
		{Index: 0, Function: chatFunctionCall{Name: "Write", Arguments: `{"file_path":`}},
	}})
	got := acc.buildToolCalls()
	if len(got) != 1 {
		t.Fatalf("got %d calls", len(got))
	}
	if _, ok := got[0].Function.Arguments["__parse_error"]; !ok {
		t.Errorf("missing __parse_error in %v", got[0].Function.Arguments)
	}
}

func TestBuildChatRequestToolCallMatching(t *testing.T) {
	req := ollama.ChatRequest{
		Model: "test-model",
		Messages: []ollama.Message{
			{
				Role: "assistant",
				ToolCalls: []ollama.MessageToolCall{
					{Function: ollama.MessageToolCallFunction{Name: "Write", Arguments: map[string]any{"file_path": "a.txt"}}},
					{Function: ollama.MessageToolCallFunction{Name: "Read", Arguments: map[string]any{"file_path": "b.txt"}}},
				},
			},
			{
				Role:     "tool",
				ToolName: "Write",
				Content:  "wrote a.txt",
			},
			{
				Role:     "tool",
				ToolName: "read", // test case-insensitive match
				Content:  "contents of b.txt",
			},
		},
	}

	converted, err := buildChatRequest(req)
	if err != nil {
		t.Fatalf("buildChatRequest failed: %v", err)
	}

	if len(converted.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(converted.Messages))
	}

	asst := converted.Messages[0]
	if len(asst.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls in assistant message, got %d", len(asst.ToolCalls))
	}
	if asst.ToolCalls[0].ID != "call_Write_0" || asst.ToolCalls[1].ID != "call_Read_1" {
		t.Fatalf("unexpected tool call IDs: %s, %s", asst.ToolCalls[0].ID, asst.ToolCalls[1].ID)
	}

	tool1 := converted.Messages[1]
	if tool1.Role != "tool" || tool1.ToolCallID != "call_Write_0" {
		t.Fatalf("expected tool1 to match call_Write_0, got %+v", tool1)
	}

	tool2 := converted.Messages[2]
	if tool2.Role != "tool" || tool2.ToolCallID != "call_Read_1" {
		t.Fatalf("expected tool2 to match call_Read_1, got %+v", tool2)
	}
}

func TestBuildChatRequestResponseFormat(t *testing.T) {
	messages := []ollama.Message{{Role: "user", Content: "hi"}}

	schemaReq, err := buildChatRequest(ollama.ChatRequest{
		Model:    "qwen3-coder",
		Messages: messages,
		Format: &ollama.ResponseFormat{
			Name:   "Edit.args",
			Schema: map[string]any{"type": "object"},
		},
	})
	if err != nil {
		t.Fatalf("buildChatRequest failed: %v", err)
	}
	if schemaReq.ResponseFormat == nil || schemaReq.ResponseFormat.Type != "json_schema" {
		t.Fatalf("expected json_schema response_format, got %+v", schemaReq.ResponseFormat)
	}
	if schemaReq.ResponseFormat.JSONSchema == nil || schemaReq.ResponseFormat.JSONSchema.Name != "Edit_args" {
		t.Fatalf("unexpected json_schema metadata: %+v", schemaReq.ResponseFormat.JSONSchema)
	}

	objReq, err := buildChatRequest(ollama.ChatRequest{
		Model:    "qwen3-coder",
		Messages: messages,
		Format:   &ollama.ResponseFormat{},
	})
	if err != nil {
		t.Fatalf("buildChatRequest failed: %v", err)
	}
	if objReq.ResponseFormat == nil || objReq.ResponseFormat.Type != "json_object" || objReq.ResponseFormat.JSONSchema != nil {
		t.Fatalf("expected json_object response_format, got %+v", objReq.ResponseFormat)
	}
}

func TestParseResponseChoiceRepairsRecoverableJSON(t *testing.T) {
	raw := "{\"file_path\":\"a.txt\",\"contents\":\"hello\nworld\",}"
	choice := chatChoice{Message: chatResponseMessage{
		ToolCalls: []chatToolCall{{Function: chatFunctionCall{Name: "Write", Arguments: raw}}},
	}}
	got := parseResponseChoice(choice)
	if len(got.ToolCalls) != 1 {
		t.Fatalf("got %d calls", len(got.ToolCalls))
	}
	args := got.ToolCalls[0].Function.Arguments
	if _, hasErr := args[ollama.ParseErrorKey]; hasErr {
		t.Fatalf("expected recoverable JSON to succeed, got %#v", args)
	}
	if args["contents"] != "hello\nworld" {
		t.Fatalf("unexpected contents: %#v", args["contents"])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestClientResponseFormatFallbackOn400(t *testing.T) {
	var seenTypes []string
	httpClient := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(r.Body)
			var req chatCompletionRequest
			_ = json.Unmarshal(raw, &req)
			if req.ResponseFormat != nil {
				seenTypes = append(seenTypes, req.ResponseFormat.Type)
			} else {
				seenTypes = append(seenTypes, "<omitted>")
			}
			switch len(seenTypes) {
			case 1:
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Status:     "400 Bad Request",
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"json_schema is not supported for this model"}}`)),
				}, nil
			case 2:
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Status:     "400 Bad Request",
					Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"response_format json_object unsupported"}}`)),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Status:     "200 OK",
					Body:       io.NopCloser(bytes.NewBufferString(`{"id":"1","choices":[{"index":0,"message":{"role":"assistant","content":"{\"ok\":true}"}}]}`)),
				}, nil
			}
		}),
	}

	client := New("http://omlx.local/v1", WithHTTPClient(httpClient))
	res, err := client.ChatSync(context.Background(), ollama.ChatRequest{
		Model:    "local-mlx",
		Messages: []ollama.Message{{Role: "user", Content: "hi"}},
		Format: &ollama.ResponseFormat{
			Name:   "Write_arguments",
			Schema: map[string]any{"type": "object"},
		},
	})
	if err != nil {
		t.Fatalf("ChatSync failed: %v", err)
	}
	if res.Content != `{"ok":true}` {
		t.Fatalf("unexpected content: %q", res.Content)
	}
	if len(seenTypes) != 3 || seenTypes[0] != "json_schema" || seenTypes[1] != "json_object" || seenTypes[2] != "<omitted>" {
		t.Fatalf("unexpected response_format downgrade sequence: %v", seenTypes)
	}
}

// Zero is the default and means "no cap": max_tokens must stay off the wire so
// the provider's own limit applies, instead of cutting a large Write call at
// 8192 tokens. A positive value still travels verbatim.
func TestBuildChatRequestMaxTokensCap(t *testing.T) {
	messages := []ollama.Message{{Role: "user", Content: "hi"}}

	uncapped, err := buildChatRequest(ollama.ChatRequest{Model: "m", Messages: messages})
	if err != nil {
		t.Fatalf("buildChatRequest failed: %v", err)
	}
	if uncapped.MaxTokens != nil {
		t.Fatalf("max_tokens = %d, want omitted when uncapped", *uncapped.MaxTokens)
	}
	payload, err := json.Marshal(uncapped)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if strings.Contains(string(payload), "max_tokens") {
		t.Fatalf("uncapped request must not carry max_tokens: %s", payload)
	}

	capped, err := buildChatRequest(ollama.ChatRequest{Model: "m", Messages: messages, Options: ollama.ChatOptions{NumPredict: 16384}})
	if err != nil {
		t.Fatalf("buildChatRequest failed: %v", err)
	}
	if capped.MaxTokens == nil || *capped.MaxTokens != 16384 {
		t.Fatalf("max_tokens = %v, want 16384", capped.MaxTokens)
	}
}
