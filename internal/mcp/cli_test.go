package mcp

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRunCLI_AddAndRemoveLocal(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// 1. Add local server
	addArgs := []string{"add", "--env", "PORT=8080", "--env", "DEBUG=true", "test-srv", "node", "index.js"}
	if err := RunCLI(configDir, cwd, addArgs); err != nil {
		t.Fatalf("RunCLI add failed: %v", err)
	}

	// Verify file was created locally
	localFile := filepath.Join(cwd, ".vibe-coder", "mcp.json")
	cfg, err := loadOrEmpty(localFile)
	if err != nil {
		t.Fatalf("failed to load local config: %v", err)
	}

	srv, ok := cfg.MCPServers["test-srv"]
	if !ok {
		t.Fatalf("expected test-srv to be configured locally")
	}

	if srv.Command != "node" {
		t.Errorf("expected command 'node', got %q", srv.Command)
	}
	if len(srv.Args) != 1 || srv.Args[0] != "index.js" {
		t.Errorf("expected args ['index.js'], got %v", srv.Args)
	}
	if srv.Env["PORT"] != "8080" || srv.Env["DEBUG"] != "true" {
		t.Errorf("unexpected env: %v", srv.Env)
	}

	// mcp.json may hold --env secrets, so it must be owner-only.
	if runtime.GOOS != "windows" {
		info, err := os.Stat(localFile)
		if err != nil {
			t.Fatalf("failed to stat local config: %v", err)
		}
		if perm := info.Mode().Perm(); perm&0o077 != 0 {
			t.Errorf("expected owner-only permissions on mcp.json, got %04o", perm)
		}
	}

	// 2. Remove local server
	removeArgs := []string{"remove", "test-srv"}
	if err := RunCLI(configDir, cwd, removeArgs); err != nil {
		t.Fatalf("RunCLI remove failed: %v", err)
	}

	cfg, err = loadOrEmpty(localFile)
	if err != nil {
		t.Fatalf("failed to load local config: %v", err)
	}
	if _, ok := cfg.MCPServers["test-srv"]; ok {
		t.Errorf("expected test-srv to be removed")
	}
}

func TestRunCLI_AddAndRemoveGlobal(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// 1. Add global server
	addArgs := []string{"add", "--global", "test-global", "python", "app.py"}
	if err := RunCLI(configDir, cwd, addArgs); err != nil {
		t.Fatalf("RunCLI add failed: %v", err)
	}

	// Verify file was created globally
	globalFile := filepath.Join(configDir, "mcp.json")
	cfg, err := loadOrEmpty(globalFile)
	if err != nil {
		t.Fatalf("failed to load global config: %v", err)
	}

	srv, ok := cfg.MCPServers["test-global"]
	if !ok {
		t.Fatalf("expected test-global to be configured globally")
	}
	if srv.Command != "python" {
		t.Errorf("expected command 'python', got %q", srv.Command)
	}

	// 2. Remove global server
	removeArgs := []string{"remove", "--global", "test-global"}
	if err := RunCLI(configDir, cwd, removeArgs); err != nil {
		t.Fatalf("RunCLI remove failed: %v", err)
	}

	cfg, err = loadOrEmpty(globalFile)
	if err != nil {
		t.Fatalf("failed to load global config: %v", err)
	}
	if _, ok := cfg.MCPServers["test-global"]; ok {
		t.Errorf("expected test-global to be removed")
	}
}

func TestRunCLI_List(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("failed to create project dir: %v", err)
	}

	// Run list with empty configs
	if err := RunCLI(configDir, cwd, []string{"list"}); err != nil {
		t.Fatalf("RunCLI list empty failed: %v", err)
	}

	// Setup some configs
	localFile := filepath.Join(cwd, ".vibe-coder", "mcp.json")
	if err := saveConfig(localFile, mcpConfigFile{
		MCPServers: map[string]ServerConfig{
			"local-srv": {Command: "echo", Args: []string{"local"}},
		},
	}); err != nil {
		t.Fatalf("failed to setup local config: %v", err)
	}

	globalFile := filepath.Join(configDir, "mcp.json")
	if err := saveConfig(globalFile, mcpConfigFile{
		MCPServers: map[string]ServerConfig{
			"global-srv": {Command: "echo", Args: []string{"global"}, Env: map[string]string{"A": "B"}},
		},
	}); err != nil {
		t.Fatalf("failed to setup global config: %v", err)
	}

	// Run list with configs
	if err := RunCLI(configDir, cwd, []string{"list"}); err != nil {
		t.Fatalf("RunCLI list populated failed: %v", err)
	}
}

func TestRunCLI_InvalidEnv(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	addArgs := []string{"add", "--env", "PORT_WITHOUT_VALUE", "test-srv", "node", "index.js"}
	err := RunCLI(configDir, cwd, addArgs)
	if err == nil {
		t.Fatal("expected error with invalid env, got nil")
	}
	if !strings.Contains(err.Error(), "invalid environment variable format") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestRunCLI_MissingArgs(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	// Missing command
	err := RunCLI(configDir, cwd, []string{"add", "test-srv"})
	if err == nil {
		t.Fatal("expected error with missing command, got nil")
	}
	if !strings.Contains(err.Error(), "missing <name> or <command>") {
		t.Errorf("unexpected error: %v", err)
	}

	// Missing server name for remove
	err = RunCLI(configDir, cwd, []string{"remove"})
	if err == nil {
		t.Fatal("expected error with missing remove target, got nil")
	}
}

func TestRunCLI_ServerNotFoundToRemove(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")

	err := RunCLI(configDir, cwd, []string{"remove", "nonexistent"})
	if err == nil {
		t.Fatal("expected error removing non-existent server, got nil")
	}
	if !strings.Contains(err.Error(), "not found in config") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestSaveConfigRestrictsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits are not enforced on Windows")
	}
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mcp.json")
	// Simulate a config file created previously with wider permissions.
	if err := os.WriteFile(path, []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatalf("failed to write raw: %v", err)
	}
	if err := saveConfig(path, mcpConfigFile{
		MCPServers: map[string]ServerConfig{
			"srv": {Command: "cmd", Env: map[string]string{"API_KEY": "secret"}},
		},
	}); err != nil {
		t.Fatalf("failed to save: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat saved config: %v", err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("expected owner-only permissions after save, got %04o", perm)
	}
}

func TestHandleListHidesEnvValues(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, "config")
	cwd := filepath.Join(tmpDir, "project")
	if err := saveConfig(filepath.Join(cwd, ".vibe-coder", "mcp.json"), mcpConfigFile{
		MCPServers: map[string]ServerConfig{
			"srv": {Command: "cmd", Env: map[string]string{"API_KEY": "supersecret", "PORT": "8080"}},
		},
	}); err != nil {
		t.Fatalf("failed to seed config: %v", err)
	}

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("failed to pipe stdout: %v", err)
	}
	os.Stdout = w
	listErr := handleList(configDir, cwd)
	_ = w.Close()
	os.Stdout = old
	if listErr != nil {
		t.Fatalf("handleList failed: %v", listErr)
	}
	out, _ := io.ReadAll(r)
	got := string(out)
	if strings.Contains(got, "supersecret") || strings.Contains(got, "8080") {
		t.Fatalf("mcp list leaked an env value:\n%s", got)
	}
	if !strings.Contains(got, "API_KEY=****") || !strings.Contains(got, "PORT=****") {
		t.Fatalf("expected masked env values, got:\n%s", got)
	}
}

func TestSaveAndLoadPreservesFileStructure(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "mcp.json")
	rawJSON := `{
  "mcpServers": {
    "srv1": {
      "command": "cmd1",
      "args": ["a1"],
      "env": {"k": "v"}
    }
  }
}`
	if err := os.WriteFile(path, []byte(rawJSON), 0o644); err != nil {
		t.Fatalf("failed to write raw: %v", err)
	}

	cfg, err := loadOrEmpty(path)
	if err != nil {
		t.Fatalf("failed to load: %v", err)
	}

	if cfg.MCPServers["srv1"].Command != "cmd1" {
		t.Fatalf("expected srv1 command to be cmd1, got %q", cfg.MCPServers["srv1"].Command)
	}

	if err := saveConfig(path, cfg); err != nil {
		t.Fatalf("failed to save: %v", err)
	}

	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read saved: %v", err)
	}

	var verify map[string]any
	if err := json.Unmarshal(saved, &verify); err != nil {
		t.Fatalf("failed to unmarshal saved JSON: %v", err)
	}

	if _, ok := verify["mcpServers"]; !ok {
		t.Errorf("missing mcpServers key in saved output")
	}
}
