// Package visionrole runs the optional "vision" role: a model bound
// explicitly with /setmodel vision that looks at images on behalf of a main
// model that cannot see them.
//
// It composes the same visual-assistant prompt and answer extraction as the
// sidecar pool, so an image described through the vision role reads exactly
// like one described through the sidecar. When no vision role is bound the
// runtime derives vision from the main model and then the sidecar instead.
package visionrole

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// Describer looks at image files with the model bound to the vision role.
// A nil *Describer is safe to use: Enabled reports false.
type Describer struct {
	client    ollama.Client
	model     string
	keepAlive int
	cache     *vision.Cache
}

// New returns a Describer for the given client and model, or nil when the
// role is unbound or no client is available. keepAlive is the seconds the
// vision model stays loaded after a description (see config.KeepAliveFor).
func New(client ollama.Client, model string, keepAlive int) *Describer {
	trimmed := strings.TrimSpace(model)
	if client == nil || trimmed == "" {
		return nil
	}
	return &Describer{client: client, model: trimmed, keepAlive: keepAlive, cache: vision.NewCache()}
}

// Enabled reports whether the vision role can describe images.
func (d *Describer) Enabled() bool {
	return d != nil && d.client != nil && d.model != ""
}

// Model returns the bound model name, or "" when the role is unbound.
func (d *Describer) Model() string {
	if d == nil {
		return ""
	}
	return d.model
}

// Describe has the vision model look at an image file and answer question.
// Results are cached per file revision and question.
func (d *Describer) Describe(ctx context.Context, imagePath, question string) (string, error) {
	if !d.Enabled() {
		return "", fmt.Errorf("the vision role is not configured")
	}
	trimmed := strings.TrimSpace(imagePath)
	if trimmed == "" {
		return "", fmt.Errorf("image path is empty")
	}
	if !vision.IsSupportedImage(trimmed) {
		return "", fmt.Errorf("unsupported image format: %s (supported formats: %s)", trimmed, vision.SupportedFormats())
	}
	if strings.TrimSpace(question) == "" {
		question = sidecar.DefaultDescribeQuestion
	}
	revision, err := vision.CacheKey(trimmed)
	if err != nil {
		return "", err
	}
	key := d.model + "|" + revision + "|" + question
	if cached, ok := d.cache.Get(key); ok {
		return cached, nil
	}
	payload, err := vision.EncodeFile(trimmed)
	if err != nil {
		return "", err
	}
	callCtx, cancel := context.WithTimeout(ctx, sidecar.CallTimeout)
	defer cancel()
	resp, err := d.client.ChatSync(callCtx, ollama.ChatRequest{
		Model: d.model,
		Messages: []ollama.Message{
			{Role: "system", Content: sidecar.VisualAssistantSystem},
			{Role: "user", Content: fmt.Sprintf("%s\n\n[image file: %s]", question, filepath.Base(trimmed)), Images: []string{payload}},
		},
		// Thinking must be off, not merely omitted: Ollama enables it by
		// default on capable models, and reasoning tokens would eat the
		// NumPredict budget, returning an empty Content instead of a
		// description.
		Think:     ollama.ThinkOff(),
		KeepAlive: d.keepAlive,
		Options:   ollama.ChatOptions{Temperature: 0, NumPredict: sidecar.DescribeNumPredict},
	})
	if err != nil {
		return "", err
	}
	answer := sidecar.ExtractAnswer(resp)
	if answer == "" {
		return "", fmt.Errorf("vision model returned empty description")
	}
	d.cache.Put(key, answer)
	return answer, nil
}
