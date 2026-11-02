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

// An OpenAI-compatible main model receives the level too (reasoning_effort, or
// the template switch for off), so the row reports it as supported instead of
// warning that it never leaves vibe.
func TestThinkingRowReportsLevelDeliveredOnOpenAI(t *testing.T) {
	cfg := thinkingCfg(t, config.ProviderOpenAI, "low")
	cfg.ThinkingKnown, cfg.ThinkingSupported = true, true
	got := ThinkingRow(cfg).Plain
	if got != "low (supported)" {
		t.Fatalf("row = %q, want low (supported)", got)
	}
	if strings.Contains(got, "not applied") {
		t.Fatalf("the level is delivered on OpenAI-compatible providers: %q", got)
	}
}

// Nothing configured means nothing to warn about: the model uses its default.
func TestThinkingRowUnsetLevelOnOpenAIIsQuiet(t *testing.T) {
	cfg := thinkingCfg(t, config.ProviderOpenAI, "")
	if got := ThinkingRow(cfg).Plain; got != "default" {
		t.Fatalf("row = %q, want a bare default", got)
	}
}

func TestThinkingRowNilConfig(t *testing.T) {
	if got := ThinkingRow(nil).Plain; got != "default" {
		t.Fatalf("row = %q", got)
	}
}
