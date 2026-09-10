package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadThinkLevelPrecedence(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))

	configPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(configPath, []byte("THINK=low\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIBE_CODER_CONFIG", configPath)

	fromFile, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if fromFile.OllamaThinkLevel != "low" {
		t.Fatalf("expected low from file, got %q", fromFile.OllamaThinkLevel)
	}

	t.Setenv("VIBE_CODER_THINK", "high")
	fromEnv, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if fromEnv.OllamaThinkLevel != "high" {
		t.Fatalf("expected env override high, got %q", fromEnv.OllamaThinkLevel)
	}

	fromCLI, err := Load([]string{"--think", "medium"})
	if err != nil {
		t.Fatal(err)
	}
	if fromCLI.OllamaThinkLevel != "medium" {
		t.Fatalf("expected CLI override medium, got %q", fromCLI.OllamaThinkLevel)
	}
}

func TestLoadThinkLevelNormalizationAndRejection(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_CONFIG", filepath.Join(tmp, "missing.env"))
	t.Setenv("VIBE_CODER_THINK", "")

	cfg, err := Load([]string{"--think-level", "HIGH"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OllamaThinkLevel != "high" {
		t.Fatalf("expected normalized high, got %q", cfg.OllamaThinkLevel)
	}

	if _, err := Load([]string{"--think", "ultra"}); err == nil {
		t.Fatal("expected invalid level error")
	} else if !strings.Contains(strings.ToLower(err.Error()), "invalid think level") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadNoThinkMapsToOff(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_CONFIG", filepath.Join(tmp, "missing.env"))
	t.Setenv("VIBE_CODER_THINK", "")

	cfg, err := Load([]string{"--no-think"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OllamaNoThink {
		t.Fatal("expected OllamaNoThink from CLI")
	}
}

func TestSaveModelSettingsThink(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(cfgPath, []byte("MODEL=a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		ConfigDir:        tmp,
		ConfigFile:       cfgPath,
		Model:            "a",
		OllamaHost:       "http://localhost:11434",
		OllamaThinkLevel: "medium",
	}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "THINK=medium") {
		t.Fatalf("expected THINK=medium, got:\n%s", string(data))
	}
	cfg.OllamaThinkLevel = ""
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfgPath)
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "THINK=") {
			t.Fatalf("expected THINK removed when unset, got:\n%s", string(data))
		}
	}
}

func TestUsageMentionsThink(t *testing.T) {
	t.Parallel()
	if got := Usage("vibe-coder"); !strings.Contains(got, "--think") {
		t.Fatalf("expected usage to document --think, got:\n%s", got)
	}
}
