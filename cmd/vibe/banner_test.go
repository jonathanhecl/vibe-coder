package main

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
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

func TestBannerFieldsWithJevstyle(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		Model:         "llama3.2:3b",
		SidecarModel:  "qwen3.5:4b",
		JevstyleModel: "decision-v1",
		OllamaHost:    "http://localhost:11434",
	}
	fields := bannerFields(cfg, "session-123", false)
	want := []bannerField{
		{Label: "Session", Value: "session-123"},
		{Label: "Model", Value: "llama3.2:3b"},
		{Label: "Sidecar", Value: "qwen3.5:4b"},
		{Label: "Ollama", Value: "http://localhost:11434"},
		{Label: "Jevstyle", Value: "decision-v1"},
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
