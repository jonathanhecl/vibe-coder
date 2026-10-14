package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnterIsolatedLayoutKeepsWritesInFolder asserts an isolated run points
// every writable path at its working directory instead of the user-level
// config and state directories.
func TestEnterIsolatedLayoutKeepsWritesInFolder(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	configDir := filepath.Join(dir, "config")
	cwd := filepath.Join(dir, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &Config{
		Isolated:    true,
		Cwd:         cwd,
		ConfigDir:   configDir,
		StateDir:    stateDir,
		ConfigFile:  filepath.Join(configDir, "vibe-coder.env"),
		PermFile:    filepath.Join(configDir, "vibe-coder.env"),
		HistoryFile: filepath.Join(stateDir, "history.txt"),
	}
	if err := cfg.EnterIsolatedLayout(); err != nil {
		t.Fatalf("EnterIsolatedLayout: %v", err)
	}

	wantConfig := filepath.Join(cwd, IsolatedConfigFileName)
	if cfg.ConfigFile != wantConfig {
		t.Errorf("ConfigFile = %q, want %q", cfg.ConfigFile, wantConfig)
	}
	if cfg.PermFile != wantConfig {
		t.Errorf("PermFile = %q, want %q", cfg.PermFile, wantConfig)
	}
	wantHistory := filepath.Join(cwd, IsolatedHistoryFileName)
	if cfg.HistoryFile != wantHistory {
		t.Errorf("HistoryFile = %q, want %q", cfg.HistoryFile, wantHistory)
	}
	if got := cfg.LogPath(); got != filepath.Join(cwd, IsolatedLogFileName) {
		t.Errorf("LogPath = %q, want the isolated log in the folder", got)
	}
	for _, path := range []string{cfg.ConfigFile, cfg.PermFile, cfg.HistoryFile, cfg.LogPath()} {
		if !strings.HasPrefix(path, cwd) {
			t.Errorf("path %q escapes the isolated folder %q", path, cwd)
		}
	}
}

// TestEnterIsolatedLayoutReadsFolderSettings asserts settings saved inside the
// folder apply to the next isolated run, and that the file cannot turn
// isolation off.
func TestEnterIsolatedLayoutReadsFolderSettings(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Isolated:      true,
		Cwd:           dir,
		OllamaHost:    "http://localhost:11434",
		RoleModels:    map[Role]RoleModel{},
		Providers:     map[string]Provider{},
		ContextWindow: 8192,
	}
	localPath := filepath.Join(dir, IsolatedConfigFileName)
	if err := os.WriteFile(localPath, []byte("MODEL=folder-model\nISOLATED=false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnterIsolatedLayout(); err != nil {
		t.Fatalf("EnterIsolatedLayout: %v", err)
	}
	if cfg.Model != "folder-model" {
		t.Errorf("Model = %q, want the folder setting", cfg.Model)
	}
	if !cfg.Isolated {
		t.Error("the folder settings file must not turn isolation off")
	}
	if !cfg.ConfigFileExists {
		t.Error("a settings file inside the folder counts as an existing configuration")
	}
}

// TestEnterIsolatedLayoutNoopWhenShared asserts a shared run keeps its
// user-level paths untouched.
func TestEnterIsolatedLayoutNoopWhenShared(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		Cwd:         filepath.Join(dir, "project"),
		ConfigDir:   filepath.Join(dir, "config"),
		StateDir:    filepath.Join(dir, "state"),
		ConfigFile:  filepath.Join(dir, "config", "vibe-coder.env"),
		HistoryFile: filepath.Join(dir, "state", "history.txt"),
	}
	if err := cfg.EnterIsolatedLayout(); err != nil {
		t.Fatalf("EnterIsolatedLayout: %v", err)
	}
	if cfg.ConfigFile != filepath.Join(dir, "config", "vibe-coder.env") {
		t.Errorf("ConfigFile = %q, want the shared path", cfg.ConfigFile)
	}
	if cfg.HistoryFile != filepath.Join(dir, "state", "history.txt") {
		t.Errorf("HistoryFile = %q, want the shared path", cfg.HistoryFile)
	}
	if got := cfg.LogPath(); got != filepath.Join(dir, "config", "vibe-coder.log") {
		t.Errorf("LogPath = %q, want the shared log", got)
	}
}

// TestEnterIsolatedLayoutRequiresCwd asserts an isolated run without a working
// directory fails instead of silently writing to the shared paths.
func TestEnterIsolatedLayoutRequiresCwd(t *testing.T) {
	cfg := &Config{Isolated: true}
	if err := cfg.EnterIsolatedLayout(); err == nil {
		t.Fatal("expected an error for an isolated run without cwd")
	}
}

func TestLogPathNilConfig(t *testing.T) {
	var cfg *Config
	if got := cfg.LogPath(); got != "" {
		t.Errorf("LogPath = %q, want empty", got)
	}
}

// TestLoadDetectsFolderIsolatedSession asserts a folder holding an isolated
// session turns the run isolated and that no user-level directory is created
// for it, which is what keeps an isolated run fully inside its folder.
func TestLoadDetectsFolderIsolatedSession(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VIBE_CODER_CONFIG", filepath.Join(dir, "vibe-coder.env"))
	t.Setenv("VIBE_CODER_ISOLATED", "")
	t.Setenv("ISOLATED", "")
	t.Chdir(dir)

	sessionPath := filepath.Join(dir, IsolatedSessionFileName)
	if err := os.WriteFile(sessionPath, []byte("{\"role\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if !cfg.Isolated {
		t.Fatal("expected the folder session to make the run isolated")
	}
	if cfg.ConfigFile != filepath.Join(dir, IsolatedConfigFileName) {
		t.Errorf("ConfigFile = %q, want the folder settings file", cfg.ConfigFile)
	}
	if cfg.HistoryFile != filepath.Join(dir, IsolatedHistoryFileName) {
		t.Errorf("HistoryFile = %q, want the folder history", cfg.HistoryFile)
	}
	for _, name := range []string{".config", ".local"} {
		if _, err := os.Stat(filepath.Join(home, name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("isolated load must not create %s under the home directory", name)
		}
	}
}

// TestLoadHonorsExplicitAlternatives asserts a folder session does not hijack
// a run that asked for another mode.
func TestLoadHonorsExplicitAlternatives(t *testing.T) {
	dir := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("VIBE_CODER_CONFIG", filepath.Join(dir, "vibe-coder.env"))
	t.Setenv("VIBE_CODER_ISOLATED", "")
	t.Setenv("ISOLATED", "")
	t.Chdir(dir)

	if err := os.WriteFile(filepath.Join(dir, IsolatedSessionFileName), []byte("{\"role\":\"user\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{{"--new"}, {"--isolated=false"}} {
		cfg, err := Load(args)
		if err != nil {
			t.Fatalf("load config %v: %v", args, err)
		}
		if cfg.Isolated {
			t.Errorf("Load(%v) must not be isolated", args)
		}
	}
}
