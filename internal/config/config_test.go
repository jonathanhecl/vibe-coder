package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	t.Setenv("VIBEGO_MODEL", "env-model")
	t.Setenv("VIBEGO_DEBUG", "true")

	cfg, err := Load([]string{
		"--ollama-host", "http://cli-host:11434",
		"-m", "cli-model",
		"--max-tokens", "4096",
		"--temperature", "0.4",
		"--context-window", "8192",
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

