package sidecar

import (
	"context"
	"errors"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// chat is the single point of contact with the sidecar model. It applies
// the worker semaphore, the per-call timeout and cancellation propagation.
func (p *Pool) chat(ctx context.Context, system, user string, numPredict int) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-p.sem }()

	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()

	resp, err := p.client.ChatSync(callCtx, ollama.ChatRequest{
		Model: p.cfg.SidecarModel,
		Messages: []ollama.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Options: ollama.ChatOptions{
			Temperature: 0,
			NumPredict:  numPredict,
		},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", errors.New("sidecar returned empty content")
	}
	return resp.Content, nil
}
