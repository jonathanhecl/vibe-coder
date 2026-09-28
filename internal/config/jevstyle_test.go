package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJevstyleInUse(t *testing.T) {
	t.Parallel()

	var cfg Config
	if cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false when empty")
	}

	cfg.JevstyleModel = "jev-v1"
	if !cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse true when model set")
	}
	if !cfg.JevstyleOllamaInUse() {
		t.Fatal("expected JevstyleOllamaInUse true when model set")
	}

	cfg.JevstyleModel = "   "
	if cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false when whitespace")
	}

	var nilCfg *Config
	if nilCfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false for nil config")
	}
}

func TestJevstyleInUseWithV3Endpoint(t *testing.T) {
	t.Parallel()

	cfg := Config{JevstyleV3Endpoint: "http://mac-mini.local:8765"}
	if !cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse true for v3 endpoint")
	}
	if !cfg.JevstyleV3InUse() {
		t.Fatal("expected JevstyleV3InUse true")
	}
	if cfg.JevstyleOllamaInUse() {
		t.Fatal("expected JevstyleOllamaInUse false for v3-only config")
	}

	cfg.JevstyleV3Endpoint = "  "
	if cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false for whitespace v3 endpoint")
	}
}

func TestApplyConfigFileJevstyleV3Endpoint(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "vibe-coder.env")
	content := strings.Join([]string{
		"JEVSTYLE_V3_ENDPOINT=http://mac-mini.local:8765",
		"ASSISTED_YES=true",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg := &Config{}
	if err := applyConfigFile(cfg, path); err != nil {
		t.Fatalf("applyConfigFile: %v", err)
	}
	if cfg.JevstyleV3Endpoint != "http://mac-mini.local:8765" {
		t.Fatalf("expected v3 endpoint loaded from file, got %q", cfg.JevstyleV3Endpoint)
	}
	if !cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse true after loading v3 endpoint")
	}
	if !cfg.AssistedYes {
		t.Fatal("expected AssistedYes true from file")
	}
}

func TestSaveModelSettingsRoundTripJevstyleV3(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "vibe-coder.env")
	cfg := &Config{
		ConfigFile:         path,
		Model:              "main-m",
		JevstyleV3Endpoint: "http://mac-mini.local:8765",
		AssistedYes:        true,
	}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded := &Config{}
	if err := applyConfigFile(loaded, path); err != nil {
		t.Fatalf("applyConfigFile: %v", err)
	}
	if loaded.JevstyleV3Endpoint != "http://mac-mini.local:8765" {
		t.Fatalf("expected round-trip v3 endpoint, got %q", loaded.JevstyleV3Endpoint)
	}
	if !loaded.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse true after round-trip")
	}
}

func TestAssistedYesConfig(t *testing.T) {
	// 1. Env precedence
	t.Setenv("VIBE_CODER_ASSISTED_YES", "true")
	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if !cfg.AssistedYes {
		t.Fatal("expected AssistedYes true from env")
	}

	// 2. CLI flag overrides
	cfg2, err := Load([]string{"--assisted-yes=false"})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if cfg2.AssistedYes {
		t.Fatal("expected AssistedYes false from CLI")
	}

	cfg3, err := Load([]string{"--assisted"})
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if !cfg3.AssistedYes {
		t.Fatal("expected AssistedYes true from --assisted alias")
	}
}

func TestSaveModelSettingsAssistedYes(t *testing.T) {
	tmp := t.TempDir()
	envPath := filepath.Join(tmp, "vibe-coder.env")
	cfg := &Config{
		ConfigFile:    envPath,
		Model:         "main-m",
		JevstyleModel: "jev-m",
		AssistedYes:   true,
	}

	if err := SaveModelSettings(cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	data, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "ASSISTED_YES=true") {
		t.Fatalf("expected ASSISTED_YES=true in %s", string(data))
	}

	// Update to false
	cfg.AssistedYes = false
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	data, err = os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(string(data), "ASSISTED_YES=false") {
		t.Fatalf("expected ASSISTED_YES=false in %s", string(data))
	}
}
