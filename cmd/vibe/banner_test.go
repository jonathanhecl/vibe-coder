package main

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func TestBannerFields(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:        "llama3.2:3b",
		SidecarModel: "qwen3.5:4b",
		OllamaHost:   "http://localhost:11434",
	}
	fields := bannerFields(cfg, "session-123", false)
	want := []bannerField{
		{Label: "Session", Value: "session-123"},
		{Label: "Model", Value: "llama3.2:3b"},
		{Label: "Sidecar", Value: "qwen3.5:4b"},
		{Label: "Ollama", Value: "http://localhost:11434"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %#v", len(want), len(fields), fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field %d = %#v, want %#v", i, fields[i], want[i])
		}
	}
}

func TestBannerFieldsResumed(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:      "llama3.2:3b",
		OllamaHost: "http://localhost:11434",
	}
	fields := bannerFields(cfg, "session-abc", true)
	if fields[0].Value != "session-abc    (resumed)" {
		t.Fatalf("expected session value to indicate resumed, got %q", fields[0].Value)
	}
}

func TestBannerFieldsTemporal(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:      "llama3.2:3b",
		OllamaHost: "http://localhost:11434",
		Temporal:   true,
	}
	fields := bannerFields(cfg, "session-tmp", false)
	if fields[0].Value != "session-tmp    (temporal)" {
		t.Fatalf("expected session value to indicate temporal, got %q", fields[0].Value)
	}
}

func TestBannerFieldsIsolated(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:      "llama3.2:3b",
		OllamaHost: "http://localhost:11434",
		Isolated:   true,
	}
	fields := bannerFields(cfg, "session-iso", false)
	if fields[0].Value != "session-iso    (isolated)" {
		t.Fatalf("expected session value to indicate isolated, got %q", fields[0].Value)
	}
}

func TestBannerFieldsWithDecision(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		SidecarModel:  "qwen3.5:4b",
		DecisionModel: "decision-v1",
		OllamaHost:    "http://localhost:11434",
	}
	fields := bannerFields(cfg, "session-123", false)
	want := []bannerField{
		{Label: "Session", Value: "session-123"},
		{Label: "Model", Value: "llama3.2:3b"},
		{Label: "Sidecar", Value: "qwen3.5:4b"},
		{Label: "Ollama", Value: "http://localhost:11434"},
		{Label: "Decision", Value: "decision-v1 (run '/yes assisted' to enable)"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %#v", len(want), len(fields), fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field %d = %#v, want %#v", i, fields[i], want[i])
		}
	}
}

func TestBannerFieldsWithDecisionAssisted(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		SidecarModel:  "qwen3.5:4b",
		DecisionModel: "decision-v1",
		AssistedYes:   true,
		OllamaHost:    "http://localhost:11434",
	}
	fields := bannerFields(cfg, "session-123", false)
	want := []bannerField{
		{Label: "Session", Value: "session-123"},
		{Label: "Model", Value: "llama3.2:3b"},
		{Label: "Sidecar", Value: "qwen3.5:4b"},
		{Label: "Ollama", Value: "http://localhost:11434"},
		{Label: "Decision", Value: "decision-v1 (assisted enabled)"},
	}
	if len(fields) != len(want) {
		t.Fatalf("expected %d fields, got %d: %#v", len(want), len(fields), fields)
	}
	for i := range want {
		if fields[i] != want[i] {
			t.Fatalf("field %d = %#v, want %#v", i, fields[i], want[i])
		}
	}
}

func TestStartupBannerDecisionAssisted(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		DecisionModel: "decision-2b:latest",
		AssistedYes:   true,
		OllamaHost:    "http://localhost:11434",
	}

	// Styled banner: (assisted enabled) in green (\x1b[32m)
	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))
	if !strings.Contains(styled, "\x1b[32m(assisted enabled)\x1b[0m") {
		t.Fatalf("expected green '(assisted enabled)' in styled banner, got:\n%s", styled)
	}
	if !strings.Contains(styled, "decision-2b:latest") {
		t.Fatalf("expected decision model in styled banner, got:\n%s", styled)
	}

	// Plain/unstyled banner
	plain := startupBanner(cfg, "session-123", false, tui.Style{})
	if !strings.Contains(plain, "(assisted enabled)") {
		t.Fatalf("expected '(assisted enabled)' in plain banner, got:\n%s", plain)
	}
}

func TestStartupBannerDecisionWithoutAssisted(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		DecisionModel: "decision-2b:latest",
		AssistedYes:   false,
		OllamaHost:    "http://localhost:11434",
	}

	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))
	if strings.Contains(styled, "(assisted enabled)") {
		t.Fatalf("did not expect '(assisted enabled)' in styled banner when AssistedYes is false, got:\n%s", styled)
	}
	if !strings.Contains(styled, "\x1b[97m(run '/yes assisted' to enable)\x1b[0m") {
		t.Fatalf("expected white '(run '/yes assisted' to enable)' in styled banner, got:\n%s", styled)
	}

	plain := startupBanner(cfg, "session-123", false, tui.Style{})
	if strings.Contains(plain, "(assisted enabled)") {
		t.Fatalf("did not expect '(assisted enabled)' in plain banner when AssistedYes is false, got:\n%s", plain)
	}
	if !strings.Contains(plain, "(run '/yes assisted' to enable)") {
		t.Fatalf("expected '(run '/yes assisted' to enable)' in plain banner, got:\n%s", plain)
	}
}

func TestBannerFieldsWithDecisionEndpoint(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:              "llama3.2:3b",
		DecisionEndpoint: "http://mac-mini.local:8765",
		OllamaHost:         "http://localhost:11434",
	}
	fields := bannerFields(cfg, "session-123", false)
	found := false
	for _, f := range fields {
		if f.Label == "Decision" {
			found = true
			if !strings.Contains(f.Value, "http://mac-mini.local:8765") {
				t.Fatalf("expected v3 endpoint in banner, got %q", f.Value)
			}
		}
	}
	if !found {
		t.Fatalf("expected Decision field for endpoint, got %#v", fields)
	}

	styled := startupBanner(cfg, "session-123", false, tui.NewStyleForTest(true))
	if !strings.Contains(styled, "http://mac-mini.local:8765") {
		t.Fatalf("expected v3 endpoint in styled banner, got:\n%s", styled)
	}
	plain := startupBanner(cfg, "session-123", false, tui.Style{})
	if !strings.Contains(plain, "http://mac-mini.local:8765") {
		t.Fatalf("expected v3 endpoint in plain banner, got:\n%s", plain)
	}
}

func TestBannerFieldsNilConfig(t *testing.T) {
	t.Parallel()

	fields := bannerFields(nil, "session-nil", false)
	if len(fields) != 4 {
		t.Fatalf("expected 4 fields, got %d", len(fields))
	}
	if fields[1].Value != "" || fields[3].Value != "" {
		t.Fatalf("expected empty model/host for nil config, got %#v", fields)
	}
	if fields[2].Value == "" {
		t.Fatalf("expected sidecar placeholder for nil config, got %#v", fields[2])
	}
}

func TestBannerWordmark(t *testing.T) {
	t.Parallel()

	lines := strings.Split(bannerWordmark, "\n")
	if len(lines) != 6 {
		t.Fatalf("expected 6 wordmark lines, got %d", len(lines))
	}
	if strings.Contains(bannerWordmark, "\x1b") {
		t.Fatal("wordmark must not contain ANSI escape codes")
	}
}

func TestBannerFieldsOffline(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:      "llama3.2:3b",
		OllamaHost: "http://localhost:11434",
		Offline:    true,
	}
	fields := bannerFields(cfg, "session-off", false)
	found := false
	for _, f := range fields {
		if f.Label == "Offline" {
			found = true
			if !strings.Contains(f.Value, "enabled") {
				t.Fatalf("expected enabled offline in banner, got %q", f.Value)
			}
		}
	}
	if !found {
		t.Fatalf("expected Offline field in banner, got %#v", fields)
	}

	plain := startupBanner(cfg, "session-off", false, tui.Style{})
	if !strings.Contains(plain, "Offline: enabled (network blocked)") {
		t.Fatalf("expected offline in plain banner, got:\n%s", plain)
	}
	styled := startupBanner(cfg, "session-off", false, tui.NewStyleForTest(true))
	if !strings.Contains(styled, "enabled (network blocked)") {
		t.Fatalf("expected offline in styled banner, got:\n%s", styled)
	}
}
