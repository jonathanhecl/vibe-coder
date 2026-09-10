package sidecar

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

// VisualAssistantSystem instructs the describing model to be a faithful eye
// for another agent. Shared by the pool path and the DescribeImage tool so
// both routes produce the same style of observation.
const VisualAssistantSystem = "You are a precise visual assistant describing an image " +
	"for another agent that cannot see it. Report only what you actually observe: " +
	"layout, objects, colors, spatial relations, visible text, and anything unusual. " +
	"Do NOT invent details, do NOT guess at what is unclear — say so instead. " +
	"Keep the description factual and structured; no prose flourishes."

// DefaultDescribeQuestion is used when the caller wants a general look.
const DefaultDescribeQuestion = "Describe this image in thorough detail: layout, objects, " +
	"colors, spatial relations, visible text, and anything unusual."

const describeNumPredict = 512

// DescribeImage has the sidecar model look at an image file and answer
// question about it. It returns plain descriptive text. Results are cached
// per file revision and question, and concurrent callers share one flight,
// following the same load-control rules as the other pool methods.
func (p *Pool) DescribeImage(ctx context.Context, imagePath, question string) (string, error) {
	if !p.Enabled() {
		return "", fmt.Errorf("sidecar is not enabled")
	}
	trimmed := strings.TrimSpace(imagePath)
	if trimmed == "" {
		return "", fmt.Errorf("image path is empty")
	}
	if !vision.IsSupportedImage(trimmed) {
		return "", fmt.Errorf("unsupported image format: %s (supported formats: %s)", trimmed, vision.SupportedFormats())
	}
	if strings.TrimSpace(question) == "" {
		question = DefaultDescribeQuestion
	}
	revision, err := vision.CacheKey(trimmed)
	if err != nil {
		return "", err
	}
	key := cacheKey("describe", p.cfg.SidecarModel, revision, question)
	if cached, ok := p.cache.get(key); ok {
		return cached, nil
	}
	v, err, _ := p.sf.Do(key, func() (any, error) {
		payload, err := vision.EncodeFile(trimmed)
		if err != nil {
			return "", err
		}
		return p.chatWithImages(ctx, VisualAssistantSystem, question, trimmed, payload)
	})
	if err != nil {
		return "", err
	}
	desc := strings.TrimSpace(v.(string))
	if desc == "" {
		return "", fmt.Errorf("sidecar returned empty description")
	}
	p.cache.put(key, desc)
	return desc, nil
}

func (p *Pool) chatWithImages(ctx context.Context, system, question, imagePath, payload string) (string, error) {
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
			{Role: "user", Content: fmt.Sprintf("%s\n\n[image file: %s]", question, filepath.Base(imagePath)), Images: []string{payload}},
		},
		Options: ollama.ChatOptions{
			Temperature: 0,
			NumPredict:  describeNumPredict,
		},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", fmt.Errorf("sidecar returned empty content")
	}
	return resp.Content, nil
}
