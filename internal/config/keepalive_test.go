package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeepAliveDefaults(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_CONFIG", filepath.Join(tmp, "does-not-exist.env"))

	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.KeepAliveMain != -1 {
		t.Fatalf("expected default KeepAliveMain -1, got %d", cfg.KeepAliveMain)
	}
	if cfg.KeepAliveWorker != 300 {
		t.Fatalf("expected default KeepAliveWorker 300, got %d", cfg.KeepAliveWorker)
	}
	if got := cfg.KeepAliveFor(RoleMain); got != -1 {
		t.Fatalf("KeepAliveFor(main) = %d want -1", got)
	}
	if got := cfg.KeepAliveFor(RoleSidecar); got != 300 {
		t.Fatalf("KeepAliveFor(sidecar) = %d want 300", got)
	}
	if got := cfg.KeepAliveFor(RoleVision); got != 300 {
		t.Fatalf("KeepAliveFor(vision) = %d want 300", got)
	}
}

func TestKeepAlivePrecedence(t *testing.T) {
	tmp := t.TempDir()
	configFile := filepath.Join(tmp, "vibe-coder.env")
	content := strings.Join([]string{
		"KEEP_ALIVE_MAIN=120",
		"KEEP_ALIVE_WORKER=240",
	}, "\n")
	if err := os.WriteFile(configFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_CONFIG", configFile)
	// env wins over the config file for the worker role.
	t.Setenv("VIBE_CODER_KEEP_ALIVE_WORKER", "60")

	cfg, err := Load([]string{"--keep-alive-main", "5"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.KeepAliveMain != 5 {
		t.Fatalf("expected CLI KeepAliveMain 5, got %d", cfg.KeepAliveMain)
	}
	if cfg.KeepAliveWorker != 60 {
		t.Fatalf("expected env KeepAliveWorker 60, got %d", cfg.KeepAliveWorker)
	}
}

func TestKeepAliveForNilSafe(t *testing.T) {
	var cfg *Config
	if got := cfg.KeepAliveFor(RoleMain); got != 0 {
		t.Fatalf("nil KeepAliveFor = %d want 0", got)
	}
}

func TestSaveModelSettingsPersistsKeepAlive(t *testing.T) {
	tmp := t.TempDir()
	configFile := filepath.Join(tmp, "vibe-coder.env")
	cfg := defaultConfig(tmp, tmp, tmp)
	cfg.ConfigFile = configFile
	cfg.KeepAliveMain = 45
	cfg.KeepAliveWorker = 90

	if err := SaveModelSettings(cfg); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "KEEP_ALIVE_MAIN=45") {
		t.Fatalf("expected KEEP_ALIVE_MAIN=45 in %q", text)
	}
	if !strings.Contains(text, "KEEP_ALIVE_WORKER=90") {
		t.Fatalf("expected KEEP_ALIVE_WORKER=90 in %q", text)
	}
}
