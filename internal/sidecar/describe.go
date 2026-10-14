package sidecar

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
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

// FocusedVisualSystem instructs the describing model to answer one specific
// question instead of narrating the whole image.
const FocusedVisualSystem = "You are a precise visual assistant answering one specific " +
	"question about an image for another agent that cannot see it. Answer ONLY the " +
	"question asked. Start with a direct verdict (yes, no, or a short value), or say " +
	"\"cannot determine\" when the image does not show it clearly. Then give at most " +
	"two short sentences of supporting visual evidence, such as where it appears or " +
	"the exact visible text. Do NOT describe anything the question does not ask " +
	"about, and do NOT invent details."

// DefaultDescribeQuestion is used when the caller wants a general look.
const DefaultDescribeQuestion = "Describe this image in thorough detail: layout, objects, " +
	"colors, spatial relations, visible text, and anything unusual."

// VisualSystemFor picks the system prompt: a focused question gets the
// answer-only persona; a blank or default question keeps the thorough
// descriptive persona.
func VisualSystemFor(question string) string {
	trimmed := strings.TrimSpace(question)
	if trimmed == "" || trimmed == DefaultDescribeQuestion {
		return VisualAssistantSystem
	}
	return FocusedVisualSystem
}

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
		return p.chatWithImages(ctx, VisualSystemFor(question), question, filepath.Base(trimmed), payload)
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

// DescribePayload has the sidecar model answer question about an image that
// already exists as a base64 payload (for example a screenshot captured in
// memory). Unlike DescribeImage it neither caches nor shares flights: every
// payload is unique. label is a short human-readable name shown to the model.
func (p *Pool) DescribePayload(ctx context.Context, payload, label, question string) (string, error) {
	if !p.Enabled() {
		return "", fmt.Errorf("sidecar is not enabled")
	}
	if strings.TrimSpace(payload) == "" {
		return "", fmt.Errorf("image payload is empty")
	}
	if strings.TrimSpace(question) == "" {
		question = DefaultDescribeQuestion
	}
	desc, err := p.chatWithImages(ctx, VisualSystemFor(question), question, label, payload)
	if err != nil {
		return "", err
	}
	desc = strings.TrimSpace(desc)
	if desc == "" {
		return "", fmt.Errorf("sidecar returned empty description")
	}
	return desc, nil
}

func (p *Pool) chatWithImages(ctx context.Context, system, question, label, payload string) (string, error) {
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
			{Role: "user", Content: fmt.Sprintf("%s\n\n[image: %s]", question, label), Images: []string{payload}},
		},
		// Thinking must be off, not merely omitted: Ollama enables it by default
		// on capable models, and reasoning tokens would eat the small
		// NumPredict budget, returning an empty Content instead of a description.
		Think:     ollama.ThinkOff(),
		KeepAlive: p.cfg.KeepAliveFor(config.RoleSidecar),
		Options: ollama.ChatOptions{
			Temperature: 0,
			NumPredict:  DescribeNumPredict,
		},
	})
	if err != nil {
		return "", err
	}
	answer := ExtractAnswer(resp)
	if answer == "" {
		return "", fmt.Errorf("sidecar returned empty content")
	}
	return answer, nil
}
