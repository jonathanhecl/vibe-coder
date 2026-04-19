package mcp

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadServerConfigsMergePrecedence(t *testing.T) {
	tmp := t.TempDir()
	cfgDir := filepath.Join(tmp, "cfg")
	cwd := filepath.Join(tmp, "project")
	if err := os.MkdirAll(filepath.Join(cfgDir), 0o755); err != nil {
		t.Fatalf("mkdir cfg: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".vibe-coder"), 0o755); err != nil {
		t.Fatalf("mkdir project cfg: %v", err)
	}
	global := `{"mcpServers":{"a":{"command":"cmdA","args":["1"]}}}`
	project := `{"mcpServers":{"a":{"command":"cmdOverride"},"b":{"command":"cmdB"}}}`
	if err := os.WriteFile(filepath.Join(cfgDir, "mcp.json"), []byte(global), 0o644); err != nil {
		t.Fatalf("write global: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".vibe-coder", "mcp.json"), []byte(project), 0o644); err != nil {
		t.Fatalf("write project: %v", err)
	}

	cfgs := LoadServerConfigs(cfgDir, cwd)
	if len(cfgs) != 2 {
		t.Fatalf("expected 2 servers, got %d", len(cfgs))
	}
	if cfgs["a"].Command != "cmdOverride" {
		t.Fatalf("expected project override, got %q", cfgs["a"].Command)
	}
}

func TestWrapToolName(t *testing.T) {
	w := WrapTool(New("s", "echo", nil, nil), "srv", ToolDef{
		Name:        "list",
		Description: "desc",
		InputSchema: map[string]any{"type": "object"},
	})
	if w.Name() != "mcp_srv_list" {
		t.Fatalf("unexpected wrapped name: %s", w.Name())
	}
	if w.Schema().Function.Name != "mcp_srv_list" {
		t.Fatalf("unexpected schema name: %s", w.Schema().Function.Name)
	}
}
