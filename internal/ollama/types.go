package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

const (
	// No fixed upper bound on the HTTP client: streaming /api/chat can run for a long time.
	// Per-request deadlines come from context (see config EffectiveChatTimeout).
	defaultHTTPTimeout = 0
	maxStreamBuffer    = 1024 * 1024
)

type Client interface {
	Chat(ctx context.Context, req ChatRequest) (<-chan Chunk, error)
	ChatSync(ctx context.Context, req ChatRequest) (ChatResponse, error)
	Tags(ctx context.Context) ([]Model, error)
	Version(ctx context.Context) (string, error)
	Pull(ctx context.Context, model string, progress func(PullEvent)) error
}

type HTTPClient struct {
	baseURL string
	http    *http.Client

	mu                  sync.Mutex
	thinkDisabledModels map[string]bool // by model name: Ollama rejected think for this model in-process
	toolsDisabledModels map[string]bool // by model name: Ollama rejected tools for this model in-process
}

type Message struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
	// Images carries base64-encoded pictures for vision-capable models
	// (Ollama /api/chat images field). Empty for text-only turns.
	Images []string `json:"images,omitempty"`
	// ToolCalls replays native function invocations on assistant turns, and
	// ToolName identifies native tool results (role "tool"), following the
	// history shape documented for /api/chat.
	ToolCalls []MessageToolCall `json:"tool_calls,omitempty"`
	ToolName  string            `json:"tool_name,omitempty"`
}

// MergeToolCalls accumulates streamed tool calls. Ollama may emit each call
// as soon as it is complete, or repeat the full list on the final chunk,
// so clients must append while dropping exact duplicates.
func MergeToolCalls(base, incoming []MessageToolCall) []MessageToolCall {
	if len(incoming) == 0 {
		return base
	}
	seen := make(map[string]struct{}, len(base)+len(incoming))
	key := func(c MessageToolCall) string {
		raw, err := json.Marshal(c.Function.Arguments)
		if err != nil {
			raw = []byte(fmt.Sprint(c.Function.Arguments))
		}
		return c.Function.Name + "\x00" + string(raw)
	}
	out := base
	for _, c := range base {
		seen[key(c)] = struct{}{}
	}
	for _, c := range incoming {
		k := key(c)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, c)
	}
	return out
}

type ChatOptions struct {
	NumCtx      int     `json:"num_ctx,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature"`
}

type ChatRequest struct {
	Model     string        `json:"model"`
	Messages  []Message     `json:"messages"`
	Stream    bool          `json:"stream"`
	Think     *ThinkSetting `json:"think,omitempty"`
	Tools     []ChatTool    `json:"tools,omitempty"`
	Options   ChatOptions   `json:"options"`
	KeepAlive int           `json:"keep_alive"`
}

// ChatTool declares one callable function to Ollama /api/chat following
// the OpenAI-style convention Ollama documents: {"type":"function",
// "function":{"name":..., "description":..., "parameters":...}}.
type ChatTool struct {
	Type     string           `json:"type"`
	Function ChatToolFunction `json:"function"`
}

type ChatToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

// MessageToolCall is one native function invocation returned by the model.
type MessageToolCall struct {
	Function MessageToolCallFunction `json:"function"`
}

type MessageToolCallFunction struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments,omitempty"`
}

// UnmarshalJSON accepts both object and stringified-JSON forms for
// arguments. Some models / Ollama versions emit
// {"name":"Read","arguments":"{\"file_path\":\"a.go\"}"} (a JSON-encoded
// string, OpenAI-style) instead of the documented object form
// {"name":"Read","arguments":{"file_path":"a.go"}}. Without this, the
// stream decoder fails with "cannot unmarshal string into Go struct
// field ... of type map[string]interface {}" and aborts the whole turn.
func (f *MessageToolCallFunction) UnmarshalJSON(data []byte) error {
	type rawCall struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	var raw rawCall
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Name = raw.Name
	if len(raw.Arguments) == 0 || string(raw.Arguments) == "null" {
		f.Arguments = nil
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(raw.Arguments, &obj); err == nil {
		f.Arguments = obj
		return nil
	}
	var encoded string
	if err := json.Unmarshal(raw.Arguments, &encoded); err != nil {
		return fmt.Errorf("tool call arguments must be an object or JSON-encoded string: %s", string(raw.Arguments))
	}
	trimmed := strings.TrimSpace(encoded)
	if trimmed == "" || trimmed == "null" {
		f.Arguments = nil
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return fmt.Errorf("tool call stringified arguments are not a JSON object: %q", encoded)
	}
	f.Arguments = decoded
	return nil
}

// ThinkSetting controls the Ollama think field. A nil *ThinkSetting omits
// the field (server default); otherwise it marshals to a boolean or a
// thinking level string ("low", "medium", "high", "max").
//
// A pointer is required because a plain bool with omitempty cannot send an
// explicit false — and omitting think is NOT the same as disabling it:
// Ollama enables thinking by default on capable models.
type ThinkSetting struct {
	// Level is "" (boolean mode), or a thinking level.
	Level string
	// Enabled selects think:true in boolean mode. Ignored when Level != "".
	Enabled bool
}

// ThinkOn requests default thinking (think:true).
func ThinkOn() *ThinkSetting { return &ThinkSetting{Enabled: true} }

// ThinkOff explicitly disables thinking (think:false, actually sent).
func ThinkOff() *ThinkSetting { return &ThinkSetting{Enabled: false} }

// ThinkLevel requests a thinking effort level (think:"low"|"medium"|"high"|"max").
func ThinkLevel(level string) *ThinkSetting { return &ThinkSetting{Level: level, Enabled: true} }

// IsActive reports whether this setting requests any thinking (boolean true
// or a level). Used to decide think-related retries.
func (t *ThinkSetting) IsActive() bool {
	if t == nil {
		return false
	}
	if t.Level != "" {
		return true
	}
	return t.Enabled
}

// NormalizeThinkLevel lowercases and validates a user-supplied thinking
// value. Accepted: off, on, low, medium, high, max (plus true/false/yes/no
// aliases and "" for unset). Returns the canonical form and validity.
func NormalizeThinkLevel(raw string) (string, bool) {
	switch v := strings.ToLower(strings.TrimSpace(raw)); v {
	case "":
		return "", true
	case "off", "false", "no", "disable", "disabled":
		return "off", true
	case "on", "true", "yes", "enable", "enabled":
		return "on", true
	case "low", "medium", "high", "max":
		return v, true
	default:
		return "", false
	}
}

// ResolveThinkSetting maps a configured think level ("" = unset) plus the
// legacy no-think flag and the detected thinking capability into the wire
// setting. Explicit levels always win; known non-thinking models get the
// field omitted so no doomed 400 round trip is attempted.
func ResolveThinkSetting(level string, noThink, known, supported bool) *ThinkSetting {
	if norm, ok := NormalizeThinkLevel(level); ok && norm != "" {
		switch norm {
		case "off":
			return ThinkOff()
		case "on":
			return ThinkOn()
		default:
			return ThinkLevel(norm)
		}
	}
	if noThink {
		return ThinkOff()
	}
	if known && !supported {
		return nil
	}
	return ThinkOn()
}

func (t ThinkSetting) MarshalJSON() ([]byte, error) {
	if t.Level != "" {
		return json.Marshal(t.Level)
	}
	return json.Marshal(t.Enabled)
}

// UnmarshalJSON accepts the same bool|string union the Ollama API uses.
func (t *ThinkSetting) UnmarshalJSON(data []byte) error {
	var b bool
	if err := json.Unmarshal(data, &b); err == nil {
		t.Level = ""
		t.Enabled = b
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return fmt.Errorf("think must be a boolean or a level string: %w", err)
	}
	norm, ok := NormalizeThinkLevel(s)
	if !ok || norm == "" {
		return fmt.Errorf("invalid think level: %q", s)
	}
	switch norm {
	case "off":
		t.Level, t.Enabled = "", false
	case "on":
		t.Level, t.Enabled = "", true
	default:
		t.Level, t.Enabled = norm, true
	}
	return nil
}

// Chunk is one streamed slice of a chat reply. Delta carries final visible
// content; Thinking carries reasoning emitted via the native Ollama field
// (when supported by the model and Ollama version). ToolCalls carries
// native function invocations when the model uses Ollama tool calling
// instead of the XML fallback envelope.
type Chunk struct {
	Delta     string
	Thinking  string
	ToolCalls []MessageToolCall
	Done      bool
	Err       error
}

type ChatResponse struct {
	Content   string
	Thinking  string
	ToolCalls []MessageToolCall
}

type Model struct {
	Name              string   `json:"name"`
	Capabilities      []string `json:"capabilities,omitempty"`
	CapabilitiesKnown bool     `json:"-"`
}

type tagsResponse struct {
	Models []tagsModel `json:"models"`
}

type tagsModel struct {
	Name         string   `json:"name"`
	Capabilities []string `json:"capabilities"`
	Details      struct {
		Capabilities []string `json:"capabilities"`
	} `json:"details"`
}

type showRequest struct {
	Model string `json:"model"`
}

type showResponse struct {
	Model        string   `json:"model"`
	Capabilities []string `json:"capabilities"`
	Details      struct {
		Capabilities []string `json:"capabilities"`
	} `json:"details"`
}

type versionResponse struct {
	Version string `json:"version"`
}

type chatResponseLine struct {
	Message struct {
		Content   string            `json:"content"`
		Thinking  string            `json:"thinking"`
		ToolCalls []MessageToolCall `json:"tool_calls"`
	} `json:"message"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

type pullRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

type PullEvent struct {
	Status    string `json:"status"`
	Completed int64  `json:"completed,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Error     string `json:"error,omitempty"`
}
