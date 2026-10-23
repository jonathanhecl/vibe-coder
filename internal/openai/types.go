package openai

import (
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// chatCompletionRequest represents the standard OpenAI chat completions request payload.
type chatCompletionRequest struct {
	Model         string         `json:"model"`
	Messages      []chatMessage  `json:"messages"`
	Stream        bool           `json:"stream"`
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
	Temperature   *float64       `json:"temperature,omitempty"`
	MaxTokens     *int           `json:"max_tokens,omitempty"`
	Tools         []chatTool     `json:"tools,omitempty"`
}

// streamOptions asks the server to append a trailing usage-only chunk.
type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

// usageReport is the provider-reported token accounting on a stream chunk
// (or non-streaming response).
type usageReport struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// serverTimings is llama.cpp's non-standard timing block, sent beside the usage
// report on the include_usage chunk. Its `predicted_per_second` is the number a
// user reads as "tokens/s"; standard OpenAI usage carries token counts only, so
// without this block the footer has no generation time to show at all.
type serverTimings struct {
	PromptN         int     `json:"prompt_n"`
	PromptMS        float64 `json:"prompt_ms"`
	PredictedN      int     `json:"predicted_n"`
	PredictedMS     float64 `json:"predicted_ms"`
	PredictedPerSec float64 `json:"predicted_per_second"`
}

// toUsage converts the wire report into the shared Usage shape, folding in a
// llama.cpp timing block when the server sent one. Durations stay zero for
// servers that report counts only, and the footer keeps omitting the speed
// segment rather than printing a wall-clock number with a different meaning.
func (u *usageReport) toUsage(t *serverTimings) *ollama.Usage {
	if u == nil {
		return nil
	}
	usage := &ollama.Usage{
		PromptTokens:     u.PromptTokens,
		CompletionTokens: u.CompletionTokens,
	}
	if t != nil {
		usage.EvalDuration = t.evalDuration()
		usage.PromptEvalDuration = msDuration(t.PromptMS)
	}
	return usage
}

// evalDuration is the generation time llama.cpp measured. The milliseconds are
// authoritative; a server that reports only the rate still yields a usable
// duration from the token count.
func (t *serverTimings) evalDuration() time.Duration {
	if t.PredictedMS > 0 {
		return msDuration(t.PredictedMS)
	}
	if t.PredictedPerSec > 0 && t.PredictedN > 0 {
		return time.Duration(float64(t.PredictedN) / t.PredictedPerSec * float64(time.Second))
	}
	return 0
}

func msDuration(ms float64) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// chatMessage represents one message in an OpenAI conversation history.
type chatMessage struct {
	Role       string         `json:"role"`
	Content    any            `json:"content"`
	Name       string         `json:"name,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

// chatContentPart represents a multimodal part in an OpenAI message.
type chatContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *chatImageURL `json:"image_url,omitempty"`
}

// chatImageURL holds the data URL for a vision input.
type chatImageURL struct {
	URL string `json:"url"`
}

// chatTool defines a tool available to the model.
type chatTool struct {
	Type     string           `json:"type"`
	Function chatToolFunction `json:"function"`
}

// chatToolFunction defines the function signature for a tool.
type chatToolFunction struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Parameters  any    `json:"parameters,omitempty"`
}

// chatToolCall represents a tool call emitted by an assistant.
type chatToolCall struct {
	Index    int              `json:"index,omitempty"`
	ID       string           `json:"id,omitempty"`
	Type     string           `json:"type,omitempty"`
	Function chatFunctionCall `json:"function,omitempty"`
}

// chatFunctionCall represents the function name and JSON arguments string.
type chatFunctionCall struct {
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// chatCompletionResponse represents a non-streaming response.
type chatCompletionResponse struct {
	ID      string       `json:"id"`
	Object  string       `json:"object"`
	Choices []chatChoice `json:"choices"`
	Error   *apiError    `json:"error,omitempty"`
}

type chatChoice struct {
	Index        int                 `json:"index"`
	Message      chatResponseMessage `json:"message"`
	FinishReason string              `json:"finish_reason"`
}

type chatResponseMessage struct {
	Role             string         `json:"role"`
	Content          string         `json:"content"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Reasoning        string         `json:"reasoning,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
}

// chatCompletionChunk represents a chunk in an SSE stream.
type chatCompletionChunk struct {
	ID      string            `json:"id"`
	Object  string            `json:"object"`
	Choices []chatChunkChoice `json:"choices"`
	Usage   *usageReport      `json:"usage,omitempty"`
	Timings *serverTimings    `json:"timings,omitempty"`
	Error   *apiError         `json:"error,omitempty"`
}

type chatChunkChoice struct {
	Index        int       `json:"index"`
	Delta        chatDelta `json:"delta"`
	FinishReason string    `json:"finish_reason"`
}

type chatDelta struct {
	Role             string         `json:"role,omitempty"`
	Content          string         `json:"content,omitempty"`
	ReasoningContent string         `json:"reasoning_content,omitempty"`
	Reasoning        string         `json:"reasoning,omitempty"`
	ToolCalls        []chatToolCall `json:"tool_calls,omitempty"`
}

type modelsResponse struct {
	Object string      `json:"object"`
	Data   []modelData `json:"data"`
	Error  *apiError   `json:"error,omitempty"`
}

type modelData struct {
	ID           string             `json:"id"`
	Object       string             `json:"object"`
	Architecture *modelArchitecture `json:"architecture,omitempty"`
	Modalities   []string           `json:"modalities,omitempty"`
	Capabilities []string           `json:"capabilities,omitempty"`
	Meta         map[string]any     `json:"meta,omitempty"`
}

type modelArchitecture struct {
	Modality         string   `json:"modality,omitempty"`
	InputModalities  []string `json:"input_modalities,omitempty"`
	OutputModalities []string `json:"output_modalities,omitempty"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}
