package openai

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// applyThinking writes the thinking level a request carries onto the wire.
// "off" disables reasoning through the chat template (the only hard switch a
// local thinking model exposes); a level travels as reasoning_effort, with
// "max" sent as "high" because that is the top standard level and servers map
// it to their own maximum (Strata turns high into its xhigh). A default-on
// request leaves both fields out: every provider already thinks by default,
// and an unknown parameter on a remote API would cost a retry.
func applyThinking(out *chatCompletionRequest, setting *ollama.ThinkSetting) {
	if setting == nil {
		return
	}
	if setting.Level != "" {
		out.ReasoningEffort = wireReasoningEffort(setting.Level)
		return
	}
	if !setting.Enabled {
		off := false
		out.ChatTemplateKwargs = &chatTemplateKwargs{EnableThinking: &off}
	}
}

// wireReasoningEffort maps a vibe thinking level to the OpenAI field value.
func wireReasoningEffort(level string) string {
	if strings.EqualFold(strings.TrimSpace(level), "max") {
		return "high"
	}
	return strings.ToLower(strings.TrimSpace(level))
}

// buildChatRequest converts an ollama.ChatRequest into an OpenAI chatCompletionRequest.
func buildChatRequest(req ollama.ChatRequest) (chatCompletionRequest, error) {
	out := chatCompletionRequest{
		Model:  req.Model,
		Stream: req.Stream,
	}
	if req.Stream {
		out.StreamOptions = &streamOptions{IncludeUsage: true}
	}

	if req.Options.Temperature > 0 {
		temp := req.Options.Temperature
		out.Temperature = &temp
	}
	if req.Options.NumPredict > 0 {
		maxTokens := req.Options.NumPredict
		out.MaxTokens = &maxTokens
	}

	applyThinking(&out, req.Think)

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
					nameLower := strings.ToLower(name)
					callIDQueues[nameLower] = append(callIDQueues[nameLower], id)

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
			nameLower := strings.ToLower(name)
			id := fmt.Sprintf("call_%s", name)
			if q := callIDQueues[nameLower]; len(q) > 0 {
				id = q[0]
				callIDQueues[nameLower] = q[1:]
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
					dataURL := toDataURL(img)
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
				if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
					args = map[string]any{
						"__parse_error":   err.Error(),
						"__raw_arguments": tc.Function.Arguments,
					}
				}
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
			if err := json.Unmarshal([]byte(argStr), &args); err != nil {
				args = map[string]any{
					"__parse_error":   err.Error(),
					"__raw_arguments": stc.args.String(),
				}
			}
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

func toDataURL(img string) string {
	if strings.HasPrefix(img, "data:") {
		return img
	}
	mime := "image/jpeg"
	if strings.HasPrefix(img, "iVBORw0KGgo") {
		mime = "image/png"
	} else if strings.HasPrefix(img, "R0lGOD") {
		mime = "image/gif"
	} else if strings.HasPrefix(img, "Qk") {
		mime = "image/bmp"
	} else if strings.HasPrefix(img, "UklGR") {
		mime = "image/webp"
	}
	return fmt.Sprintf("data:%s;base64,%s", mime, img)
}
