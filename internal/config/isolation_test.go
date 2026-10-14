package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEnterIsolatedLayoutKeepsRecordsInFolder asserts an isolated run keeps
// the records it still writes inside its working directory: the prompt history
// moves into the folder, while the settings stay in the shared config file and
// no log is written at all.
func TestEnterIsolatedLayoutKeepsRecordsInFolder(t *testing.T) {
	dir := t.TempDir()
	stateDir := filepath.Join(dir, "state")
	configDir := filepath.Join(dir, "config")
	cwd := filepath.Join(dir, "project")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}

	sharedConfig := filepath.Join(configDir, "vibe-coder.env")
	cfg := &Config{
		Isolated:    true,
		Cwd:         cwd,
		ConfigDir:   configDir,
		StateDir:    stateDir,
		ConfigFile:  sharedConfig,
		PermFile:    sharedConfig,
		HistoryFile: filepath.Join(stateDir, "history.txt"),
	}
	if err := cfg.EnterIsolatedLayout(); err != nil {
		t.Fatalf("EnterIsolatedLayout: %v", err)
	}

	wantHistory := filepath.Join(cwd, IsolatedHistoryFileName)
	if cfg.HistoryFile != wantHistory {
		t.Errorf("HistoryFile = %q, want %q", cfg.HistoryFile, wantHistory)
	}
	if !strings.HasPrefix(cfg.HistoryFile, cwd) {
		t.Errorf("history %q escapes the isolated folder %q", cfg.HistoryFile, cwd)
	}
	if cfg.ConfigFile != sharedConfig || cfg.PermFile != sharedConfig {
		t.Errorf("isolated settings must stay in the shared config, got %q and %q", cfg.ConfigFile, cfg.PermFile)
	}
	if got := cfg.LogPath(); got != "" {
		t.Errorf("LogPath = %q, want no log for an isolated run", got)
	}
}

// TestLogPathSkipsTemporal asserts a temporal run records no log either, so it
// leaves no trace of its arguments.
func TestLogPathSkipsTemporal(t *testing.T) {
	cfg := &Config{Temporal: true, ConfigDir: t.TempDir()}
	if got := cfg.LogPath(); got != "" {
		t.Errorf("LogPath = %q, want no log for a temporal run", got)
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
	if cfg.ConfigFile != filepath.Join(dir, "vibe-coder.env") {
		t.Errorf("ConfigFile = %q, want the shared config file", cfg.ConfigFile)
	}
	if cfg.LogPath() != "" {
		t.Errorf("LogPath = %q, want no log for an isolated run", cfg.LogPath())
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
