package mcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// stubServerConfig points a config entry at the in-repo stub server.
func stubServerConfig() ServerConfig {
	return ServerConfig{
		Command: os.Args[0],
		Args:    []string{"-test.run=TestStubServerProcess"},
		Env:     map[string]string{stubServerEnv: "1"},
	}
}

// writeConfig writes an mcp.json under dir with the given servers.
func writeConfig(t *testing.T, dir string, servers map[string]ServerConfig) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	data, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), data, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestLoadServerConfigsOrderedScopeAndOverride pins the two properties the
// startup report depends on: a deterministic project-first order, and a project
// entry overriding the global one while keeping its scope.
func TestLoadServerConfigsOrderedScopeAndOverride(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cfg")
	cwd := filepath.Join(tmp, "project")

	writeConfig(t, cfgDir, map[string]ServerConfig{
		"zulu":  {Command: "global-zulu"},
		"alpha": {Command: "global-alpha"},
	})
	writeConfig(t, filepath.Join(cwd, ".vibe-coder"), map[string]ServerConfig{
		"zulu": {Command: "project-zulu"},
		"beta": {Command: "project-beta"},
	})

	entries := LoadServerConfigsOrdered(cfgDir, cwd)
	got := make([]string, 0, len(entries))
	scopes := map[string]string{}
	commands := map[string]string{}
	for _, e := range entries {
		got = append(got, e.Name)
		scopes[e.Name] = e.Scope
		commands[e.Name] = e.Config.Command
	}

	want := []string{"beta", "zulu", "alpha"}
	if len(got) != len(want) {
		t.Fatalf("servers = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("servers = %v, want %v", got, want)
		}
	}
	if scopes["zulu"] != ScopeProject {
		t.Errorf("zulu scope = %q, want %q (project overrides global)", scopes["zulu"], ScopeProject)
	}
	if commands["zulu"] != "project-zulu" {
		t.Errorf("zulu command = %q, want the project definition", commands["zulu"])
	}
	if scopes["alpha"] != ScopeGlobal {
		t.Errorf("alpha scope = %q, want %q", scopes["alpha"], ScopeGlobal)
	}
}

// TestInitAllIsBestEffortPerServer is the regression test for the aborted load:
// a broken server must not take the healthy ones down with it.
func TestInitAllIsBestEffortPerServer(t *testing.T) {
	cwd := t.TempDir()
	writeConfig(t, filepath.Join(cwd, ".vibe-coder"), map[string]ServerConfig{
		// Sorts before the broken one, so the old abort-on-first-failure order
		// would have dropped it.
		"healthy": stubServerConfig(),
		"broken":  {Command: "vibe-mcp-definitely-missing-binary"},
	})

	mgr := InitAll(context.Background(), t.TempDir(), cwd)
	t.Cleanup(mgr.Stop)

	if got := mgr.Total(); got != 2 {
		t.Fatalf("total = %d, want 2", got)
	}
	if got := mgr.Connected(); got != 1 {
		t.Fatalf("connected = %d, want 1", got)
	}
	if got := mgr.Failed(); got != 1 {
		t.Fatalf("failed = %d, want 1", got)
	}
	if toolsList := mgr.Tools(); len(toolsList) == 0 {
		t.Fatal("the healthy server exposed no tools")
	}

	var failed ServerReport
	for _, r := range mgr.Reports() {
		if r.Name == "broken" {
			failed = r
		}
	}
	if failed.State != StateFailed {
		t.Fatalf("broken state = %q, want %q", failed.State, StateFailed)
	}
	if failed.Err == "" {
		t.Fatal("a failed server must carry its reason into the report")
	}
	// The failure must survive rendering, not only the struct.
	if len(ReportRows(mgr.Reports())) != 2 {
		t.Fatalf("reports should list both servers, got %d rows", len(ReportRows(mgr.Reports())))
	}
}

// TestInitAllDeduplicatesSanitizedToolNames covers two distinct server names
// that sanitize into the same function-name prefix.
func TestInitAllDeduplicatesSanitizedToolNames(t *testing.T) {
	cwd := t.TempDir()
	writeConfig(t, filepath.Join(cwd, ".vibe-coder"), map[string]ServerConfig{
		"a.b": stubServerConfig(),
		"a_b": stubServerConfig(),
	})

	mgr := InitAll(context.Background(), t.TempDir(), cwd)
	t.Cleanup(mgr.Stop)
	if got := mgr.Connected(); got != 2 {
		t.Fatalf("connected = %d, want 2", got)
	}

	seen := map[string]bool{}
	for _, tool := range mgr.Tools() {
		name := tool.Name()
		if seen[name] {
			t.Fatalf("duplicate model-facing tool name %q", name)
		}
		seen[name] = true
	}
	total := len(mgr.Tools())
	if total != 6 {
		t.Fatalf("expected 6 tools (3 per server), got %d", total)
	}
	if !seen["mcp_a_b_echo"] || !seen["mcp_a_b_echo_2"] {
		t.Fatalf("expected the colliding names to be disambiguated, got %v", seen)
	}
}

// TestNilManagerIsSafe keeps the nil checks honest: the status surfaces may ask
// a Manager that was never created.
func TestNilManagerIsSafe(t *testing.T) {
	var mgr *Manager
	mgr.Stop()
	if mgr.Total() != 0 || mgr.Connected() != 0 || mgr.Failed() != 0 {
		t.Fatal("a nil Manager should report nothing")
	}
	if mgr.Reports() != nil || mgr.Tools() != nil {
		t.Fatal("a nil Manager should return no reports or tools")
	}
}
