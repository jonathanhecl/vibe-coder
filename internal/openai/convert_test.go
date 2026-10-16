package openai

import (
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

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
