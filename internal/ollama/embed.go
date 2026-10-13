package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Embedder is implemented by clients that can turn text into embeddings.
//
// It is deliberately separate from Client: producing embeddings is an optional
// capability. The native Ollama client implements it; a client that speaks a
// protocol without an embedding endpoint (for example the OpenAI-compatible
// adapter) still satisfies Client and simply does not satisfy Embedder, so the
// consumer can fall back to text-only retrieval.
type Embedder interface {
	// Embed returns one vector per input, in the same order as the inputs.
	Embed(ctx context.Context, model string, inputs []string) ([][]float64, error)
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}

// Embed calls the native Ollama /api/embed endpoint with a batch of inputs.
// Sending the whole batch in one request keeps indexing and querying cheap.
func (c *HTTPClient) Embed(ctx context.Context, model string, inputs []string) ([][]float64, error) {
	if strings.TrimSpace(model) == "" {
		return nil, fmt.Errorf("embedding model is required")
	}
	if len(inputs) == 0 {
		return nil, nil
	}
	payload, err := json.Marshal(embedRequest{Model: model, Input: inputs})
	if err != nil {
		return nil, fmt.Errorf("encode embed request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/embed", bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.applyAuth(req)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body := readLimitedBody(resp.Body, 32*1024)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama embed failed: %s (%s)", resp.Status, strings.TrimSpace(body))
	}
	var decoded embedResponse
	if err := json.Unmarshal([]byte(body), &decoded); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}
	if len(decoded.Embeddings) != len(inputs) {
		return nil, fmt.Errorf("ollama embed returned %d vectors for %d inputs", len(decoded.Embeddings), len(inputs))
	}
	return decoded.Embeddings, nil
}
