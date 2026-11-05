package openai

import (
	"encoding/json"
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
