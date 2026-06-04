package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestResolveDirs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		goos         string
		home         string
		localAppData string
		wantConfig   string
		wantState    string
		wantErr      bool
	}{
		{
			name:         "windows uses local app data",
			goos:         "windows",
			localAppData: `C:\Users\test\AppData\Local`,
			wantConfig:   filepath.Join(`C:\Users\test\AppData\Local`, "vibe-coder"),
			wantState:    filepath.Join(`C:\Users\test\AppData\Local`, "vibe-coder"),
		},
		{
			name:       "linux uses xdg-like defaults",
			goos:       "linux",
			home:       "/home/tester",
			wantConfig: filepath.Join("/home/tester", ".config", "vibe-coder"),
			wantState:  filepath.Join("/home/tester", ".local", "state", "vibe-coder"),
		},
		{
			name:    "windows without local app data fails",
			goos:    "windows",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotConfig, gotState, err := resolveDirs(tc.goos, tc.home, tc.localAppData)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotConfig != tc.wantConfig {
				t.Fatalf("config dir mismatch: got %q want %q", gotConfig, tc.wantConfig)
			}
			if gotState != tc.wantState {
				t.Fatalf("state dir mismatch: got %q want %q", gotState, tc.wantState)
			}
		})
	}
}

func TestLoadPrecedenceAndDirs(t *testing.T) {
	tmp := t.TempDir()
	localAppData := filepath.Join(tmp, "localapp")
	configFile := filepath.Join(tmp, "custom.env")

	content := strings.Join([]string{
		`MODEL="file-model"`,
		`OLLAMA_HOST="http://file-host:11434"`,
		`MAX_TOKENS="1024"`,
		`TEMPERATURE="0.2"`,
		`CONTEXT_WINDOW="2048"`,
	}, "\n")
	if err := os.WriteFile(configFile, []byte(content), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("VIBE_CODER_CONFIG", configFile)
	t.Setenv("OLLAMA_HOST", "http://env-host:11434")
	t.Setenv("VIBE_CODER_MODEL", "env-model")
	t.Setenv("VIBE_CODER_SIDECAR_MODEL", "env-sidecar")
	t.Setenv("VIBE_CODER_DEBUG", "true")

	cfg, err := Load([]string{
		"--interactive",
		"--ollama-host", "http://cli-host:11434",
		"-m", "cli-model",
		"--max-tokens", "4096",
		"--temperature", "0.4",
		"--context-window", "8192",
		"--sidecar", "cli-sidecar",
		"-y",
		"--session-id", "abc123",
	})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	if cfg.OllamaHost != "http://cli-host:11434" {
		t.Fatalf("expected CLI ollama host, got %q", cfg.OllamaHost)
	}
	if cfg.Model != "cli-model" {
		t.Fatalf("expected CLI model, got %q", cfg.Model)
	}
	if cfg.SidecarModel != "cli-sidecar" {
		t.Fatalf("expected CLI sidecar model, got %q", cfg.SidecarModel)
	}
	if cfg.MaxTokens != 4096 {
		t.Fatalf("expected CLI max tokens, got %d", cfg.MaxTokens)
	}
	if cfg.Temperature != 0.4 {
		t.Fatalf("expected CLI temperature, got %v", cfg.Temperature)
	}
	if cfg.ContextWindow != 8192 {
		t.Fatalf("expected CLI context window, got %d", cfg.ContextWindow)
	}
	if !cfg.YesMode {
		t.Fatal("expected yes mode true from CLI")
	}
	if cfg.SessionID != "abc123" {
		t.Fatalf("expected session id from CLI, got %q", cfg.SessionID)
	}
	if !cfg.Interactive {
		t.Fatal("expected interactive true from CLI")
	}
	if !cfg.ConfigFileExists {
		t.Fatal("expected config file to be marked as existing")
	}
	if !cfg.Debug {
		t.Fatal("expected debug true from env")
	}

	if _, err := os.Stat(cfg.ConfigDir); err != nil {
		t.Fatalf("config dir not created: %v", err)
	}
	if _, err := os.Stat(cfg.StateDir); err != nil {
		t.Fatalf("state dir not created: %v", err)
	}
	if _, err := os.Stat(cfg.SessionsDir); err != nil {
		t.Fatalf("sessions dir not created: %v", err)
	}
}

func TestAutoDetectModelWhenEmpty(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("VIBE_CODER_MODEL", "")
	t.Setenv("VIBEGO_MODEL", "")
	t.Setenv("VIBE_CODER_RAM_GB", "32")

	cfg, err := Load([]string{})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Model != "qwen3.5:9b" {
		t.Fatalf("unexpected auto model: %q", cfg.Model)
	}
}

func TestLoadHelpAndVersionFlags(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)

	cfg, err := Load([]string{"--help", "--version"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.ShowHelp {
		t.Fatal("expected show help to be true")
	}
	if !cfg.ShowVer {
		t.Fatal("expected show version to be true")
	}
}

func TestLoadInteractiveFlagOrderWithPrompt(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)

	first, err := Load([]string{"-i", "-p", "hello"})
	if err != nil {
		t.Fatalf("load config (-i then -p): %v", err)
	}
	if !first.Interactive {
		t.Fatal("expected interactive=true when -i appears before -p")
	}
	if first.Prompt != "hello" {
		t.Fatalf("expected prompt=hello, got %q", first.Prompt)
	}

	second, err := Load([]string{"-p", "hello", "-i"})
	if err != nil {
		t.Fatalf("load config (-p then -i): %v", err)
	}
	if !second.Interactive {
		t.Fatal("expected interactive=true when -i appears after -p")
	}
	if second.Prompt != "hello" {
		t.Fatalf("expected prompt=hello, got %q", second.Prompt)
	}
}

func TestLoadConfigFileExistsFalseWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "missing.env")
	t.Setenv("LOCALAPPDATA", tmp)
	t.Setenv("VIBE_CODER_CONFIG", cfgPath)

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ConfigFileExists {
		t.Fatalf("expected ConfigFileExists=false for %s", cfgPath)
	}
}

func TestSaveModelSettingsPreservesOtherConfig(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	initial := strings.Join([]string{
		"# keep this comment",
		"MAX_TOKENS=2048",
		"MODEL=old-model",
		"SIDECAR_MODEL=old-sidecar",
		"OLLAMA_HOST=http://localhost:11434",
		"",
	}, "\n")
	if err := os.WriteFile(cfgPath, []byte(initial), 0o600); err != nil {
		t.Fatalf("write initial config: %v", err)
	}

	cfg := &Config{
		ConfigDir:    tmp,
		ConfigFile:   cfgPath,
		Model:        "qwen3.5:9b",
		SidecarModel: "qwen3.5:4b",
		OllamaHost:   "http://192.168.1.50:11434",
	}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatalf("save model settings: %v", err)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	saved := string(data)
	for _, want := range []string{
		"# keep this comment",
		"MAX_TOKENS=2048",
		"MODEL=qwen3.5:9b",
		"SIDECAR_MODEL=qwen3.5:4b",
		"OLLAMA_HOST=http://192.168.1.50:11434",
	} {
		if !strings.Contains(saved, want) {
			t.Fatalf("saved config missing %q:\n%s", want, saved)
		}
	}
	if strings.Contains(saved, "SIDECAR_DISABLED") {
		t.Fatalf("did not expect SIDECAR_DISABLED when SidecarDisabled is false:\n%s", saved)
	}
}

func TestSaveModelSettingsSidecarDisabled(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(cfgPath, []byte("MODEL=a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		ConfigDir:       tmp,
		ConfigFile:      cfgPath,
		Model:           "a",
		SidecarModel:    "s",
		OllamaHost:      "http://localhost:11434",
		SidecarDisabled: true,
	}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "SIDECAR_DISABLED=true") {
		t.Fatalf("expected SIDECAR_DISABLED=true, got:\n%s", string(data))
	}
	cfg.SidecarDisabled = false
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfgPath)
	if strings.Contains(string(data), "SIDECAR_DISABLED") {
		t.Fatalf("expected SIDECAR_DISABLED removed, got:\n%s", string(data))
	}
}

func TestEffectiveChatTimeout(t *testing.T) {
	t.Parallel()
	if d := (&Config{}).EffectiveChatTimeout(); d != 15*time.Minute {
		t.Fatalf("default: got %v want 15m", d)
	}
	if d := (&Config{ChatTimeout: 30 * time.Second}).EffectiveChatTimeout(); d != 30*time.Second {
		t.Fatalf("custom: got %v", d)
	}
}

func TestLoadChatTimeoutFromEnv(t *testing.T) {
	tmp := t.TempDir()
	localAppData := filepath.Join(tmp, "localapp")
	t.Setenv("LOCALAPPDATA", localAppData)
	t.Setenv("VIBE_CODER_CHAT_TIMEOUT", "45s")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ChatTimeout != 45*time.Second {
		t.Fatalf("ChatTimeout: got %v", cfg.ChatTimeout)
	}
	if cfg.EffectiveChatTimeout() != 45*time.Second {
		t.Fatalf("EffectiveChatTimeout: got %v", cfg.EffectiveChatTimeout())
	}
}

func TestLoadNoThinkFromEnvAndCLI(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_NO_THINK", "true")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OllamaNoThink {
		t.Fatal("expected OllamaNoThink from env")
	}
	t.Setenv("VIBE_CODER_NO_THINK", "")
	cfg2, err := Load([]string{"--no-think"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg2.OllamaNoThink {
		t.Fatal("expected OllamaNoThink from CLI")
	}
}

func TestLoadUIModePrecedence(t *testing.T) {
	tmp := t.TempDir()
	configFile := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(configFile, []byte("UI=plain\n"), 0o600); err != nil {
		t.Fatalf("write config file: %v", err)
	}

	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_CONFIG", configFile)
	t.Setenv("VIBE_CODER_UI", "rich")

	fromEnv, err := Load(nil)
	if err != nil {
		t.Fatalf("load config from env: %v", err)
	}
	if fromEnv.UI != "rich" {
		t.Fatalf("expected env UI override, got %q", fromEnv.UI)
	}

	fromCLI, err := Load([]string{"--ui", "plain"})
	if err != nil {
		t.Fatalf("load config from cli: %v", err)
	}
	if fromCLI.UI != "plain" {
		t.Fatalf("expected cli UI override, got %q", fromCLI.UI)
	}
}

func TestLoadInvalidUIMode(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)

	_, err := Load([]string{"--ui", "neon"})
	if err == nil {
		t.Fatal("expected invalid UI mode error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "invalid ui mode") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadLongFlagAliases(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)

	cfg, err := Load([]string{"--prompt", "hello world", "--yes"})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Prompt != "hello world" {
		t.Fatalf("expected Prompt='hello world', got %q", cfg.Prompt)
	}
	if !cfg.YesMode {
		t.Fatal("expected YesMode=true")
	}
}

func TestLoadHideThinkFromEnvAndCLI(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))
	t.Setenv("VIBE_CODER_HIDE_THINK", "true")
	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.OllamaHideThink {
		t.Fatal("expected OllamaHideThink from env HIDE_THINK")
	}

	t.Setenv("VIBE_CODER_HIDE_THINK", "")
	t.Setenv("VIBE_CODER_HIDE_THINKING", "true")
	cfg2, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg2.OllamaHideThink {
		t.Fatal("expected OllamaHideThink from env HIDE_THINKING")
	}

	t.Setenv("VIBE_CODER_HIDE_THINKING", "")
	cfg3, err := Load([]string{"--hide-think"})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg3.OllamaHideThink {
		t.Fatal("expected OllamaHideThink from CLI")
	}
}

func TestSaveModelSettingsHideThink(t *testing.T) {
	tmp := t.TempDir()
	cfgPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(cfgPath, []byte("MODEL=a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := &Config{
		ConfigDir:       tmp,
		ConfigFile:      cfgPath,
		Model:           "a",
		SidecarModel:    "s",
		OllamaHost:      "http://localhost:11434",
		OllamaHideThink: true,
	}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "HIDE_THINK=true") {
		t.Fatalf("expected HIDE_THINK=true, got:\n%s", string(data))
	}
	cfg.OllamaHideThink = false
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "HIDE_THINK=false") {
		t.Fatalf("expected HIDE_THINK=false, got:\n%s", string(data))
	}
}

func TestLoadMigratesLegacyConfig(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	
	// Create legacy config.env
	legacyPath := filepath.Join(tmp, "vibe-coder", "config.env")
	if err := os.MkdirAll(filepath.Dir(legacyPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyPath, []byte("MODEL=my-migrated-model\n"), 0644); err != nil {
		t.Fatal(err)
	}
	
	// Load config (which should trigger migration to vibe-coder.env)
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	
	if cfg.Model != "my-migrated-model" {
		t.Fatalf("expected migrated model, got %q", cfg.Model)
	}
	
	// Check that vibe-coder.env exists and config.env is gone (or renamed)
	newPath := filepath.Join(tmp, "vibe-coder", "vibe-coder.env")
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("expected vibe-coder.env to exist: %v", err)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatal("expected legacy config.env to be removed/renamed")
	}
}


