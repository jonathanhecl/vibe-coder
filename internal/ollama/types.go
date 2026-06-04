package ollama

import (
	"context"
	"net/http"
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
}

type Message struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

type ChatOptions struct {
	NumCtx      int     `json:"num_ctx,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
	Temperature float64 `json:"temperature,omitempty"`
}

type ChatRequest struct {
	Model     string      `json:"model"`
	Messages  []Message   `json:"messages"`
	Stream    bool        `json:"stream"`
	Think     bool        `json:"think,omitempty"`
	Options   ChatOptions `json:"options"`
	KeepAlive int         `json:"keep_alive"`
}

// Chunk is one streamed slice of a chat reply. Delta carries final visible
// content; Thinking carries reasoning emitted via the native Ollama field
// (when supported by the model and Ollama version).
type Chunk struct {
	Delta    string
	Thinking string
	Done     bool
	Err      error
}

type ChatResponse struct {
	Content  string
	Thinking string
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
		Content  string `json:"content"`
		Thinking string `json:"thinking"`
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
