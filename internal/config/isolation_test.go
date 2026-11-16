package config

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestEnterIsolatedLayoutKeepsRecordsInFolder asserts an isolated run keeps
// the records it still writes inside its working directory while prompt
// recall stays in memory (seeded from the transcript, no history file), and
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
	// A legacy history file from an older run must be left untouched: it may
	// hold the only surviving prompts of already-compacted sessions.
	legacyHistory := filepath.Join(cwd, ".vibe-isolated-history.txt")
	if err := os.WriteFile(legacyHistory, []byte("old prompt\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cfg.EnterIsolatedLayout(); err != nil {
		t.Fatalf("EnterIsolatedLayout: %v", err)
	}

	if cfg.HistoryFile != "" {
		t.Errorf("HistoryFile = %q, want empty (in-memory recall only)", cfg.HistoryFile)
	}
	if _, err := os.Stat(legacyHistory); err != nil {
		t.Errorf("legacy isolated history file must be preserved, stat err = %v", err)
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

// TestLocalTempDirOnlyForSandboxed runs asserts only isolated and temporal runs
// get a folder-local scratch directory, and a normal run keeps the OS temp dir.
func TestLocalTempDirOnlyForSandboxedRuns(t *testing.T) {
	cwd := t.TempDir()
	want := filepath.Join(cwd, LocalTempDirName)
	cases := []struct {
		name      string
		cfg       *Config
		want      string
		sandboxed bool
	}{
		{"shared", &Config{Cwd: cwd}, "", false},
		{"isolated", &Config{Cwd: cwd, Isolated: true}, want, true},
		{"temporal", &Config{Cwd: cwd, Temporal: true}, want, true},
		{"no cwd", &Config{Isolated: true}, "", true},
	}
	for _, c := range cases {
		if got := c.cfg.LocalTempDir(); got != c.want {
			t.Errorf("%s: LocalTempDir() = %q, want %q", c.name, got, c.want)
		}
		if got := c.cfg.Sandboxed(); got != c.sandboxed {
			t.Errorf("%s: Sandboxed() = %t, want %t", c.name, got, c.sandboxed)
		}
	}
	var nilCfg *Config
	if nilCfg.Sandboxed() || nilCfg.LocalTempDir() != "" || nilCfg.Mode() != "" {
		t.Error("a nil config must not be sandboxed")
	}
	if got := (&Config{Isolated: true}).Mode(); got != "isolated" {
		t.Errorf("Mode() = %q, want isolated", got)
	}
	if got := (&Config{Temporal: true}).Mode(); got != "temporal" {
		t.Errorf("Mode() = %q, want temporal", got)
	}
}

// TestEnterLocalTempRedirectsTempEnv asserts a sandboxed run points TMPDIR, TMP
// and TEMP at a folder-local directory, so this process and every command it
// spawns write temporary files inside the working directory instead of /tmp,
// and that the directory is removed when the run ends.
func TestEnterLocalTempRedirectsTempEnv(t *testing.T) {
	cwd := t.TempDir()
	cfg := &Config{Cwd: cwd, Isolated: true}
	want := filepath.Join(cwd, LocalTempDirName)

	// Register restoration of the process env this test changes.
	for _, key := range []string{"TMPDIR", "TMP", "TEMP", sandboxTempEnv} {
		t.Setenv(key, os.Getenv(key))
	}

	dir, err := cfg.EnterLocalTemp()
	if err != nil {
		t.Fatalf("EnterLocalTemp: %v", err)
	}
	if dir != want {
		t.Fatalf("EnterLocalTemp() = %q, want %q", dir, want)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("local temp dir missing: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Errorf("local temp dir mode = %o, want 700", perm)
	}
	for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if got := os.TempDir(); got != want {
		t.Errorf("os.TempDir() = %q, want %q", got, want)
	}
	if got := SandboxTempDir(); got != want {
		t.Errorf("SandboxTempDir() = %q, want %q", got, want)
	}

	if err := cfg.CleanupLocalTemp(); err != nil {
		t.Fatalf("CleanupLocalTemp: %v", err)
	}
	if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("local temp dir must be removed on exit, stat err = %v", err)
	}
}

// TestEnterLocalTempNoopWhenShared asserts a normal run leaves the user's temp
// env alone.
func TestEnterLocalTempNoopWhenShared(t *testing.T) {
	cwd := t.TempDir()
	before := os.Getenv("TMPDIR")
	t.Setenv("TMPDIR", before)
	cfg := &Config{Cwd: cwd}
	dir, err := cfg.EnterLocalTemp()
	if err != nil || dir != "" {
		t.Fatalf("EnterLocalTemp() = %q, %v; want no-op", dir, err)
	}
	if got := os.Getenv("TMPDIR"); got != before {
		t.Errorf("TMPDIR = %q, want the shared value %q untouched", got, before)
	}
	if got := SandboxTempDir(); got != "" {
		t.Errorf("SandboxTempDir() = %q, want empty outside a sandboxed run", got)
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
	if cfg.HistoryFile != "" {
		t.Errorf("HistoryFile = %q, want empty for an isolated run", cfg.HistoryFile)
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
