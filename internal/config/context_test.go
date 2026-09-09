package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadContextFilesPrecedence(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", filepath.Join(tmp, "localapp"))

	envFile := filepath.Join(tmp, "env.md")
	cliFile := filepath.Join(tmp, "cli.txt")
	cfgFile := filepath.Join(tmp, "cfg.md")
	for _, p := range []string{envFile, cliFile, cfgFile} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	configPath := filepath.Join(tmp, "vibe-coder.env")
	if err := os.WriteFile(configPath, []byte("CONTEXT="+cfgFile+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VIBE_CODER_CONFIG", configPath)
	t.Setenv("VIBE_CODER_CONTEXT", envFile)

	cfg, err := Load([]string{"--context", cliFile, "--context", cliFile})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	// Precedence is additive: config file < env < CLI, all accumulated.
	want := []string{cfgFile, envFile, cliFile, cliFile}
	if len(cfg.ContextFiles) != len(want) {
		t.Fatalf("expected %v, got %v", want, cfg.ContextFiles)
	}
	for i := range want {
		if cfg.ContextFiles[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, cfg.ContextFiles)
		}
	}
}

func TestUsageMentionsContext(t *testing.T) {
	t.Parallel()
	if got := Usage("vibe-coder"); !strings.Contains(got, "--context") {
		t.Fatalf("expected usage to document --context, got:\n%s", got)
	}
}
