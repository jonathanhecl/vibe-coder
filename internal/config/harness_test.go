package config

import (
	"os"
	"path/filepath"
	"strings"
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

func TestSkillsDirsFromFileEnvAndFlag(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vibe-coder.env")
	list := string(os.PathListSeparator)
	if err := os.WriteFile(path, []byte("SKILLS_DIRS=from-file"+list+"other-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIBE_CODER_CONFIG", path)

	cfg, err := Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SkillsDirs) != 2 || cfg.SkillsDirs[0] != filepath.Join(cfg.Cwd, "from-file") {
		t.Fatalf("expected file dirs resolved against cwd, got %v", cfg.SkillsDirs)
	}

	// Env replaces the file value entirely.
	t.Setenv("VIBE_CODER_SKILLS_DIRS", "~/skills-env"+list+"rel-env")
	cfg, err = Load(nil)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if len(cfg.SkillsDirs) != 2 ||
		cfg.SkillsDirs[0] != filepath.Join(home, "skills-env") ||
		cfg.SkillsDirs[1] != filepath.Join(cfg.Cwd, "rel-env") {
		t.Fatalf("expected env dirs with ~ expansion and cwd resolution, got %v", cfg.SkillsDirs)
	}

	// The flag replaces both.
	cfg, err = Load([]string{"--skills-dirs", "flag-dir"})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SkillsDirs) != 1 || cfg.SkillsDirs[0] != filepath.Join(cfg.Cwd, "flag-dir") {
		t.Fatalf("expected flag to replace env, got %v", cfg.SkillsDirs)
	}
}

func TestSkillsDirsDedupeAndBlanks(t *testing.T) {
	dirs := normalizeSkillsDirs("/base", []string{"", "a", "a/", " b ", "b"})
	want := []string{"/base/a", "/base/b"}
	if len(dirs) != len(want) {
		t.Fatalf("got %v, want %v", dirs, want)
	}
	for i := range want {
		if dirs[i] != want[i] {
			t.Fatalf("got %v, want %v", dirs, want)
		}
	}
}

// SaveModelSettings preserves unknown keys, so a file-level SKILLS_DIRS
// must survive a save.
func TestSkillsDirsSurvivesSave(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vibe-coder.env")
	if err := os.WriteFile(path, []byte("SKILLS_DIRS=/opt/skills\nMODEL=old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig(dir, dir, dir)
	cfg.ConfigFile = path
	if err := applyConfigFile(cfg, path); err != nil {
		t.Fatal(err)
	}
	if err := SaveModelSettings(cfg); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "SKILLS_DIRS=/opt/skills") {
		t.Fatalf("expected SKILLS_DIRS to survive save, got:\n%s", data)
	}
}
