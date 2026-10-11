package openai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// buildChatRequest converts an ollama.ChatRequest into an OpenAI chatCompletionRequest.
func buildChatRequest(req ollama.ChatRequest) (chatCompletionRequest, error) {
	out := chatCompletionRequest{
		Model:  req.Model,
		Stream: req.Stream,
	}

	if req.Options.Temperature > 0 {
		temp := req.Options.Temperature
		out.Temperature = &temp
	}
	if req.Options.NumPredict > 0 {
		maxTokens := req.Options.NumPredict
		out.MaxTokens = &maxTokens
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]chatTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			out.Tools = append(out.Tools, chatTool{
				Type: "function",
				Function: chatToolFunction{
					Name:        t.Function.Name,
					Description: t.Function.Description,
					Parameters:  t.Function.Parameters,
				},
			})
		}
	}

	// Tool call ID tracker for matching role="tool" messages to preceding assistant tool_calls.
	callIDQueues := make(map[string][]string)

	out.Messages = make([]chatMessage, 0, len(req.Messages))
	for _, m := range req.Messages {
		switch m.Role {
		case "assistant":
			var calls []chatToolCall
			if len(m.ToolCalls) > 0 {
				calls = make([]chatToolCall, 0, len(m.ToolCalls))
				for i, tc := range m.ToolCalls {
					name := strings.TrimSpace(tc.Function.Name)
					id := fmt.Sprintf("call_%s_%d", name, i)
					callIDQueues[name] = append(callIDQueues[name], id)

					argBytes, err := json.Marshal(tc.Function.Arguments)
					argStr := "{}"
					if err == nil && len(argBytes) > 0 {
						argStr = string(argBytes)
					}

					calls = append(calls, chatToolCall{
						ID:   id,
						Type: "function",
						Function: chatFunctionCall{
							Name:      name,
							Arguments: argStr,
						},
					})
				}
			}
			out.Messages = append(out.Messages, chatMessage{
				Role:      "assistant",
				Content:   m.Content,
				ToolCalls: calls,
			})

		case "tool":
			name := strings.TrimSpace(m.ToolName)
			id := fmt.Sprintf("call_%s", name)
			if q := callIDQueues[name]; len(q) > 0 {
				id = q[0]
				callIDQueues[name] = q[1:]
			}
			out.Messages = append(out.Messages, chatMessage{
				Role:       "tool",
				Name:       name,
				ToolCallID: id,
				Content:    m.Content,
			})

		case "user":
			if len(m.Images) > 0 {
				parts := make([]chatContentPart, 0, 1+len(m.Images))
				if m.Content != "" {
					parts = append(parts, chatContentPart{
						Type: "text",
						Text: m.Content,
					})
				}
				for _, img := range m.Images {
					dataURL := img
					if !strings.HasPrefix(dataURL, "data:") {
						dataURL = "data:image/png;base64," + img
					}
					parts = append(parts, chatContentPart{
						Type:     "image_url",
						ImageURL: &chatImageURL{URL: dataURL},
					})
				}
				out.Messages = append(out.Messages, chatMessage{
					Role:    "user",
					Content: parts,
				})
			} else {
				out.Messages = append(out.Messages, chatMessage{
					Role:    "user",
					Content: m.Content,
				})
			}

		default:
			out.Messages = append(out.Messages, chatMessage{
				Role:    m.Role,
				Content: m.Content,
			})
		}
	}

	return out, nil
}

// parseResponseChoice converts a non-streaming chatChoice into an ollama.ChatResponse.
func parseResponseChoice(choice chatChoice) ollama.ChatResponse {
	msg := choice.Message
	thinking := msg.ReasoningContent
	if thinking == "" {
		thinking = msg.Reasoning
	}

	var toolCalls []ollama.MessageToolCall
	if len(msg.ToolCalls) > 0 {
		toolCalls = make([]ollama.MessageToolCall, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			name := strings.TrimSpace(tc.Function.Name)
			if name == "" {
				continue
			}
			var args map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			if args == nil {
				args = map[string]any{}
			}
			toolCalls = append(toolCalls, ollama.MessageToolCall{
				Function: ollama.MessageToolCallFunction{
					Name:      name,
					Arguments: args,
				},
			})
		}
	}

	return ollama.ChatResponse{
		Content:   msg.Content,
		Thinking:  thinking,
		ToolCalls: toolCalls,
	}
}

type streamAccumulator struct {
	toolCalls map[int]*streamedToolCall
}

type streamedToolCall struct {
	name strings.Builder
	args strings.Builder
}

func newStreamAccumulator() *streamAccumulator {
	return &streamAccumulator{
		toolCalls: make(map[int]*streamedToolCall),
	}
}

func (s *streamAccumulator) accumulate(delta chatDelta) {
	for _, tc := range delta.ToolCalls {
		idx := tc.Index
		stc, exists := s.toolCalls[idx]
		if !exists {
			stc = &streamedToolCall{}
			s.toolCalls[idx] = stc
		}
		if tc.Function.Name != "" {
			stc.name.WriteString(tc.Function.Name)
		}
		if tc.Function.Arguments != "" {
			stc.args.WriteString(tc.Function.Arguments)
		}
	}
}

func (s *streamAccumulator) buildToolCalls() []ollama.MessageToolCall {
	if len(s.toolCalls) == 0 {
		return nil
	}
	indices := make([]int, 0, len(s.toolCalls))
	for idx := range s.toolCalls {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	out := make([]ollama.MessageToolCall, 0, len(indices))
	for _, idx := range indices {
		stc := s.toolCalls[idx]
		name := strings.TrimSpace(stc.name.String())
		if name == "" {
			continue
		}
		var args map[string]any
		argStr := strings.TrimSpace(stc.args.String())
		if argStr != "" {
			_ = json.Unmarshal([]byte(argStr), &args)
		}
		if args == nil {
			args = map[string]any{}
		}
		out = append(out, ollama.MessageToolCall{
			Function: ollama.MessageToolCallFunction{
				Name:      name,
				Arguments: args,
			},
		})
	}
	return out
}
