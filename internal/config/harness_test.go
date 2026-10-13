package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestHarnessDefaults(t *testing.T) {
	t.Parallel()
	cfg := defaultConfig(t.TempDir(), t.TempDir(), t.TempDir())
	if cfg.Harness.KeepToolResults != defaultKeepToolResults {
		t.Fatalf("KeepToolResults default = %d", cfg.Harness.KeepToolResults)
	}
	if cfg.Harness.ToolOutputMaxTokens != 0 {
		t.Fatalf("ToolOutputMaxTokens default = %d", cfg.Harness.ToolOutputMaxTokens)
	}
	if cfg.Harness.CompactAtPercent != defaultCompactAtPercent {
		t.Fatalf("CompactAtPercent default = %d", cfg.Harness.CompactAtPercent)
	}
	if cfg.Harness.MetricsDisabled {
		t.Fatal("MetricsDisabled default should be false")
	}
}

func TestHarnessFromConfigFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "vibe-coder.env")
	content := "KEEP_TOOL_RESULTS=3\n" +
		"TOOL_OUTPUT_MAX_TOKENS=512\n" +
		"COMPACT_AT_PERCENT=70\n" +
		"METRICS_DISABLED=true\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig(dir, dir, dir)
	if err := applyConfigFile(cfg, path); err != nil {
		t.Fatalf("applyConfigFile: %v", err)
	}
	cfg.Harness.normalize()
	if cfg.Harness.KeepToolResults != 3 {
		t.Fatalf("KeepToolResults = %d", cfg.Harness.KeepToolResults)
	}
	if cfg.Harness.ToolOutputMaxTokens != 512 {
		t.Fatalf("ToolOutputMaxTokens = %d", cfg.Harness.ToolOutputMaxTokens)
	}
	if cfg.Harness.CompactAtPercent != 70 {
		t.Fatalf("CompactAtPercent = %d", cfg.Harness.CompactAtPercent)
	}
	if !cfg.Harness.MetricsDisabled {
		t.Fatal("expected MetricsDisabled")
	}
}

func TestHarnessEnvOverridesFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vibe-coder.env")
	if err := os.WriteFile(path, []byte("KEEP_TOOL_RESULTS=3\nCOMPACT_AT_PERCENT=70\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIBE_CODER_KEEP_TOOL_RESULTS", "9")
	t.Setenv("VIBE_CODER_METRICS_DISABLED", "yes")

	cfg := defaultConfig(dir, dir, dir)
	if err := applyConfigFile(cfg, path); err != nil {
		t.Fatalf("applyConfigFile: %v", err)
	}
	applyEnv(cfg)
	cfg.Harness.normalize()

	if cfg.Harness.KeepToolResults != 9 {
		t.Fatalf("env should override file: KeepToolResults = %d", cfg.Harness.KeepToolResults)
	}
	if cfg.Harness.CompactAtPercent != 70 {
		t.Fatalf("file value should stand when env unset: CompactAtPercent = %d", cfg.Harness.CompactAtPercent)
	}
	if !cfg.Harness.MetricsDisabled {
		t.Fatal("expected MetricsDisabled from env")
	}
}

func TestHarnessClamping(t *testing.T) {
	t.Parallel()

	h := HarnessConfig{KeepToolResults: 0, ToolOutputMaxTokens: -5, CompactAtPercent: 10}
	h.normalize()
	if h.KeepToolResults != defaultKeepToolResults {
		t.Fatalf("KeepToolResults <1 should reset to default, got %d", h.KeepToolResults)
	}
	if h.ToolOutputMaxTokens != 0 {
		t.Fatalf("negative ToolOutputMaxTokens should clamp to 0, got %d", h.ToolOutputMaxTokens)
	}
	if h.CompactAtPercent != minCompactAtPercent {
		t.Fatalf("CompactAtPercent below range should clamp to %d, got %d", minCompactAtPercent, h.CompactAtPercent)
	}

	h = HarnessConfig{KeepToolResults: 2, CompactAtPercent: 99}
	h.normalize()
	if h.CompactAtPercent != maxCompactAtPercent {
		t.Fatalf("CompactAtPercent above range should clamp to %d, got %d", maxCompactAtPercent, h.CompactAtPercent)
	}
	if h.KeepToolResults != 2 {
		t.Fatalf("valid KeepToolResults must be kept, got %d", h.KeepToolResults)
	}
}
