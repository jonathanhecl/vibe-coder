package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// TestPrintMCPBlockIsSilentWithoutServers keeps the startup output clean for
// users who never configured an MCP server.
func TestPrintMCPBlockIsSilentWithoutServers(t *testing.T) {
	mgr := mcp.InitAll(context.Background(), t.TempDir(), t.TempDir())

	var buf bytes.Buffer
	printMCPBlock(&buf, tui.Style{}, mgr)
	if buf.Len() != 0 {
		t.Fatalf("expected no output without configured servers, got %q", buf.String())
	}
}

// TestPrintMCPBlockReportsFailedServer proves the startup path tells the user a
// server did not come up, instead of only its tools quietly missing.
func TestPrintMCPBlockReportsFailedServer(t *testing.T) {
	cwd := t.TempDir()
	dir := filepath.Join(cwd, ".vibe-coder")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"mcpServers":{"broken":{"command":"vibe-mcp-definitely-missing-binary"}}}`
	if err := os.WriteFile(filepath.Join(dir, "mcp.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	mgr := mcp.InitAll(context.Background(), t.TempDir(), cwd)
	t.Cleanup(mgr.Stop)

	var buf bytes.Buffer
	printMCPBlock(&buf, tui.Style{}, mgr)
	out := buf.String()
	if !strings.Contains(out, "broken") {
		t.Fatalf("startup block should name the server, got %q", out)
	}
	if !strings.Contains(out, "failed") {
		t.Fatalf("startup block should report the failure, got %q", out)
	}
}
