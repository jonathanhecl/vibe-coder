package slash

import (
	"bytes"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type fakeMCPReporter struct{ reports []mcp.ServerReport }

func (f fakeMCPReporter) Reports() []mcp.ServerReport { return f.reports }

func newMCPCtx(t *testing.T, reporter MCPReporter) (*Ctx, *bytes.Buffer) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{ContextWindow: 32768, Cwd: tmp, SessionsDir: tmp}
	var out bytes.Buffer
	return &Ctx{Cfg: cfg, Session: session.New(cfg), Out: &out, MCP: reporter}, &out
}

func mcpFixture() []mcp.ServerReport {
	return []mcp.ServerReport{
		{
			Name: "live", Scope: mcp.ScopeProject, State: mcp.StateConnected,
			ToolNames: []string{"mcp_live_echo", "mcp_live_big"},
		},
		{
			Name: "dead", Scope: mcp.ScopeGlobal, State: mcp.StateFailed,
			Err: "fork/exec /nope: no such file or directory",
		},
	}
}

// TestMCPCommandWithoutServers points the user at the CLI instead of printing an
// empty table.
func TestMCPCommandWithoutServers(t *testing.T) {
	ctx, out := newMCPCtx(t, nil)
	handled, shouldExit, err := Dispatch(ctx, "/mcp")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("No MCP servers configured.")) {
		t.Fatalf("expected the empty-state message, got %q", got)
	}
	if !bytes.Contains(out.Bytes(), []byte("vibe mcp add")) {
		t.Fatalf("expected the add hint, got %q", got)
	}
}

// TestMCPCommandReportsStatus covers the connected and the failed server in one
// report.
func TestMCPCommandReportsStatus(t *testing.T) {
	ctx, out := newMCPCtx(t, fakeMCPReporter{reports: mcpFixture()})
	if _, _, err := Dispatch(ctx, "/mcp"); err != nil {
		t.Fatalf("dispatch /mcp: %v", err)
	}
	got := out.String()
	for _, want := range []string{"live: connected · 2 tools", "dead: failed · fork/exec", "project", "global"} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Errorf("/mcp output is missing %q:\n%s", want, got)
		}
	}
}

// TestMCPCommandToolsListsWrappedNames keeps a server with no tools out of the
// listing.
func TestMCPCommandToolsListsWrappedNames(t *testing.T) {
	ctx, out := newMCPCtx(t, fakeMCPReporter{reports: mcpFixture()})
	if _, _, err := Dispatch(ctx, "/mcp tools"); err != nil {
		t.Fatalf("dispatch /mcp tools: %v", err)
	}
	got := out.String()
	if !bytes.Contains(out.Bytes(), []byte("mcp_live_echo, mcp_live_big")) {
		t.Fatalf("expected the wrapped tool names, got %q", got)
	}
	if bytes.Contains(out.Bytes(), []byte("dead")) {
		t.Fatalf("a server without tools should not be listed, got %q", got)
	}
}

// TestMCPCommandRejectsUnknownOption proves a typo is answered, not ignored.
func TestMCPCommandRejectsUnknownOption(t *testing.T) {
	ctx, out := newMCPCtx(t, fakeMCPReporter{reports: mcpFixture()})
	handled, shouldExit, err := Dispatch(ctx, "/mcp bogus")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !bytes.Contains(out.Bytes(), []byte("Usage: /mcp [tools]")) {
		t.Fatalf("expected the usage line, got %q", out.String())
	}
}
