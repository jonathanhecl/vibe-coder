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

	cfg.JevstyleModel = "   "
	if cfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false when whitespace")
	}

	var nilCfg *Config
	if nilCfg.JevstyleInUse() {
		t.Fatal("expected JevstyleInUse false for nil config")
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

