package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// vibeBinPath is the once-per-package built binary used by the CLI flow tests.
var vibeBinPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "vibe-cli-e2e-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "temp dir: %v\n", err)
		os.Exit(1)
	}
	vibeBinPath = filepath.Join(dir, "vibe")
	if runtime.GOOS == "windows" {
		vibeBinPath += ".exe"
	}
	build := exec.Command("go", "build", "-o", vibeBinPath, "./cmd/vibe")
	build.Dir = filepath.Clean("../..")
	if out, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build vibe: %v\n%s", err, out)
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// cliTestEnv returns the process environment with the app's config env vars
// removed and HOME/USERPROFILE pointed at an isolated directory, so the test
// never reads or writes the developer's real config.
func cliTestEnv(home string) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, e := range os.Environ() {
		key, _, _ := strings.Cut(e, "=")
		upper := strings.ToUpper(key)
		if upper == "VIBE_CODER_CONFIG" || upper == "CONFIG" || strings.HasPrefix(upper, "VIBE_CODER_") {
			continue
		}
		env = append(env, e)
	}
	return append(env,
		"HOME="+home,
		"USERPROFILE="+home,
		"LOCALAPPDATA="+filepath.Join(home, "AppData", "Local"),
	)
}

func runVibe(t *testing.T, dir string, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(vibeBinPath, args...)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestCLIFlowsMCPAndSkills(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	env := cliTestEnv(home)

	out, err := runVibe(t, proj, env, "--version")
	if err != nil {
		t.Fatalf("--version: %v\n%s", err, out)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "vibe") {
		t.Fatalf("unexpected --version output: %q", out)
	}

	out, err = runVibe(t, proj, env, "--help")
	if err != nil || !strings.Contains(out, "Usage:") {
		t.Fatalf("--help missing usage: %v\n%s", err, out)
	}

	// Local MCP server: add, list (with secret masking), permissions, remove.
	if out, err = runVibe(t, proj, env, "mcp", "add", "--env", "API_KEY=supersecret", "--env", "PORT=8080", "weather", "node", "server.js"); err != nil {
		t.Fatalf("mcp add: %v\n%s", err, out)
	}
	localMCP := filepath.Join(proj, ".vibe-coder", "mcp.json")
	if _, err := os.Stat(localMCP); err != nil {
		t.Fatalf("local mcp.json not created: %v", err)
	}

	out, err = runVibe(t, proj, env, "mcp", "list")
	if err != nil {
		t.Fatalf("mcp list: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Server: weather") {
		t.Fatalf("mcp list missing weather server:\n%s", out)
	}
	if strings.Contains(out, "supersecret") {
		t.Fatalf("mcp list leaked an env secret:\n%s", out)
	}
	if !strings.Contains(out, "API_KEY=****") {
		t.Fatalf("mcp list should mask env values:\n%s", out)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(localMCP)
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Fatalf("local mcp.json mode %04o is not owner-only", perm)
		}
	}

	if out, err = runVibe(t, proj, env, "mcp", "remove", "weather"); err != nil {
		t.Fatalf("mcp remove: %v\n%s", err, out)
	}
	if out, err = runVibe(t, proj, env, "mcp", "list"); err != nil || strings.Contains(out, "Server: weather") {
		t.Fatalf("weather server was not removed: %v\n%s", err, out)
	}

	// Global MCP server.
	if out, err = runVibe(t, proj, env, "mcp", "add", "--global", "logger", "python", "logger.py"); err != nil {
		t.Fatalf("global mcp add: %v\n%s", err, out)
	}
	if out, err = runVibe(t, proj, env, "mcp", "list"); err != nil || !strings.Contains(out, "Server: logger") {
		t.Fatalf("global server not listed: %v\n%s", err, out)
	}
	if out, err = runVibe(t, proj, env, "mcp", "remove", "--global", "logger"); err != nil {
		t.Fatalf("global mcp remove: %v\n%s", err, out)
	}

	// Skills.
	src := filepath.Join(t.TempDir(), "source.md")
	if err := os.WriteFile(src, []byte("# My Skill\n\nDo the thing.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err = runVibe(t, proj, env, "skill", "add", "my-skill", src); err != nil {
		t.Fatalf("skill add: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(proj, ".vibe-coder", "skills", "my-skill.md")); err != nil {
		t.Fatalf("skill file not created: %v", err)
	}
	if out, err = runVibe(t, proj, env, "skill", "list"); err != nil || !strings.Contains(out, "Skill: my-skill") {
		t.Fatalf("skill list missing my-skill: %v\n%s", err, out)
	}
}
