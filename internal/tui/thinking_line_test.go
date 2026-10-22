package tui

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// thinkingCfg builds a config whose main role is bound to provider with the
// given kind, so the row renderer can be exercised without a full Load.
func thinkingCfg(t *testing.T, kind config.ProviderKind, level string) *config.Config {
	t.Helper()
	cfg := &config.Config{Model: "swift-1.5", OllamaThinkLevel: level}
	if err := cfg.AddProvider(config.Provider{Name: "local", Endpoint: "http://127.0.0.1:8080/v1", Kind: kind}); err != nil {
		t.Fatalf("add provider: %v", err)
	}
	if err := cfg.SetRoleModel(config.RoleMain, "local", "swift-1.5"); err != nil {
		t.Fatalf("bind main role: %v", err)
	}
	return cfg
}

func TestThinkingRowLevels(t *testing.T) {
	cases := []struct {
		name  string
		level string
		note  string
		want  string
	}{
		{name: "unset", level: "", want: "default"},
		{name: "explicit", level: "low", want: "low"},
		{name: "off", level: "off", want: "off"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := thinkingCfg(t, config.ProviderOllama, tc.level)
			if got := ThinkingRow(cfg).Plain; got != tc.want {
				t.Fatalf("row = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestThinkingRowLegacyNoThinkReadsOff(t *testing.T) {
	cfg := thinkingCfg(t, config.ProviderOllama, "")
	cfg.OllamaNoThink = true
	if got := ThinkingRow(cfg).Plain; got != "off" {
		t.Fatalf("row = %q, want off", got)
	}
}

func TestThinkingRowCapabilitySuffix(t *testing.T) {
	cfg := thinkingCfg(t, config.ProviderOllama, "medium")
	cfg.ThinkingKnown, cfg.ThinkingSupported = true, true
	if got := ThinkingRow(cfg).Plain; got != "medium (supported)" {
		t.Fatalf("row = %q", got)
	}

	cfg.ThinkingSupported = false
	if got := ThinkingRow(cfg).Plain; got != "medium (not supported by the model)" {
		t.Fatalf("row = %q", got)
	}
}

// An OpenAI-compatible main model never receives the level, so the row says so
// instead of implying the effort is applied.
func TestThinkingRowReportsUndeliverableLevel(t *testing.T) {
	cfg := thinkingCfg(t, config.ProviderOpenAI, "low")
	cfg.ThinkingKnown, cfg.ThinkingSupported = true, true
	got := ThinkingRow(cfg).Plain
	if !strings.Contains(got, "low") || !strings.Contains(got, "not applied") {
		t.Fatalf("row = %q, want the level plus the undeliverable note", got)
	}
	if strings.Contains(got, "supported") {
		t.Fatalf("a level that cannot be delivered must not be reported as supported: %q", got)
	}
}

// Nothing configured means nothing to warn about: the model uses its default.
func TestThinkingRowUnsetLevelOnOpenAIIsQuiet(t *testing.T) {
	cfg := thinkingCfg(t, config.ProviderOpenAI, "")
	if got := ThinkingRow(cfg).Plain; got != "default" {
		t.Fatalf("row = %q, want a bare default", got)
	}
	if note := ThinkingProviderNote(cfg); note != "" {
		t.Fatalf("expected no note without a configured level, got %q", note)
	}
}

func TestThinkingRowNilConfig(t *testing.T) {
	if got := ThinkingRow(nil).Plain; got != "default" {
		t.Fatalf("row = %q", got)
	}
}
