package openai

// chatCompletionRequest represents the standard OpenAI chat completions request payload.
type chatCompletionRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Stream      bool          `json:"stream"`
	Temperature *float64      `json:"temperature,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Tools       []chatTool    `json:"tools,omitempty"`
}

// chatMessage represents one message in an OpenAI conversation history.
type chatMessage struct {
	Role         string         `json:"role"`
	Content      any            `json:"content"`
	Name         string         `json:"name,omitempty"`
	ToolCalls    []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
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
	ID     string `json:"id"`
	Object string `json:"object"`
}

type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
	Code    any    `json:"code"`
}
