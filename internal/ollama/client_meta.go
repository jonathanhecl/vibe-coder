package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

func (c *HTTPClient) Tags(ctx context.Context) ([]Model, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/tags", nil)
	if err != nil {
		return nil, fmt.Errorf("build tags request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("ollama tags: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags failed: %s (%s)", resp.Status, readLimitedBody(resp.Body, 16*1024))
	}

	var out tagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode tags response: %w", err)
	}
	models := make([]Model, 0, len(out.Models))
	for _, m := range out.Models {
		unique := normalizeCapabilities(m.Capabilities, m.Details.Capabilities)
		models = append(models, Model{
			Name:              m.Name,
			Capabilities:      unique,
			CapabilitiesKnown: len(unique) > 0,
		})
	}
	return models, nil
}

func (c *HTTPClient) Show(ctx context.Context, model string) (Model, error) {
	model = strings.TrimSpace(model)
	if model == "" {
		return Model{}, errors.New("show model is required")
	}
	payload, err := json.Marshal(showRequest{Model: model})
	if err != nil {
		return Model{}, fmt.Errorf("marshal show request: %w", err)
	}
	resp, err := doPOSTWithRetry(ctx, c.http, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/show", bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		return req, nil
	})
	if err != nil {
		return Model{}, fmt.Errorf("ollama show: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Model{}, fmt.Errorf("ollama show failed: %s (%s)", resp.Status, readLimitedBody(resp.Body, 16*1024))
	}
	var out showResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Model{}, fmt.Errorf("decode show response: %w", err)
	}
	caps := normalizeCapabilities(out.Capabilities, out.Details.Capabilities)
	name := strings.TrimSpace(out.Model)
	if name == "" {
		name = model
	}
	return Model{
		Name:              name,
		Capabilities:      caps,
		CapabilitiesKnown: len(caps) > 0,
	}, nil
}

func (m Model) SupportsTools() bool {
	for _, cap := range m.Capabilities {
		c := strings.TrimSpace(strings.ToLower(cap))
		if c == "tools" || c == "tool-calling" || c == "function-calling" {
			return true
		}
	}
	return false
}

func FilterToolCapableModels(models []Model) []Model {
	out := make([]Model, 0, len(models))
	for _, model := range models {
		if model.SupportsTools() {
			out = append(out, model)
		}
	}
	return out
}

func ResolveModelCapabilities(ctx context.Context, client Client, models []Model) []Model {
	out := make([]Model, len(models))
	copy(out, models)
	inspector, ok := client.(interface {
		Show(context.Context, string) (Model, error)
	})
	if !ok {
		return out
	}
	for i := range out {
		name := strings.TrimSpace(out[i].Name)
		if name == "" {
			continue
		}
		info, err := inspector.Show(ctx, name)
		if err != nil {
			continue
		}
		caps := normalizeCapabilities(out[i].Capabilities, info.Capabilities)
		out[i].Capabilities = caps
		out[i].CapabilitiesKnown = len(caps) > 0 || out[i].CapabilitiesKnown || info.CapabilitiesKnown
	}
	return out
}

func normalizeCapabilities(groups ...[]string) []string {
	total := 0
	for _, g := range groups {
		total += len(g)
	}
	seen := map[string]struct{}{}
	unique := make([]string, 0, total)
	for _, group := range groups {
		for _, cap := range group {
			trimmed := strings.TrimSpace(strings.ToLower(cap))
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			unique = append(unique, trimmed)
		}
	}
	return unique
}

func (c *HTTPClient) Version(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/version", nil)
	if err != nil {
		return "", fmt.Errorf("build version request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("ollama version: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ollama version failed: %s (%s)", resp.Status, readLimitedBody(resp.Body, 16*1024))
	}

	var out versionResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("decode version response: %w", err)
	}
	return out.Version, nil
}

func (c *HTTPClient) Pull(ctx context.Context, model string, progress func(PullEvent)) error {
	payload, err := json.Marshal(pullRequest{Model: model, Stream: true})
	if err != nil {
		return fmt.Errorf("marshal pull request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/pull", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build pull request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ollama pull: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama pull failed: %s (%s)", resp.Status, readLimitedBody(resp.Body, 16*1024))
	}

	scanner := newStreamScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev PullEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return fmt.Errorf("decode pull event: %w", err)
		}
		if progress != nil {
			progress(ev)
		}
		if ev.Error != "" {
			return errors.New(ev.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read pull stream: %w", err)
	}
	return nil
}
