package mcp

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func renderPlainRows(rows []tui.Row) string {
	var b strings.Builder
	tui.WriteRows(&b, tui.Style{}, rows)
	return b.String()
}

// TestReportRowsStates pins the wording the startup block and /mcp share.
func TestReportRowsStates(t *testing.T) {
	reports := []ServerReport{
		{Name: "many", Scope: ScopeProject, State: StateConnected, ToolNames: []string{"a", "b", "c"}},
		{Name: "one", Scope: ScopeProject, State: StateConnected, ToolNames: []string{"a"}},
		{Name: "none", Scope: ScopeProject, State: StateConnected},
		{Name: "dead", Scope: ScopeGlobal, State: StateFailed, Err: "start mcp server dead: exec: \"nope\": executable file not found"},
	}

	out := renderPlainRows(ReportRows(reports))

	for _, want := range []string{
		"many: connected · 3 tools",
		"one: connected · 1 tool",
		"none: connected · no tools",
		"dead: failed · start mcp server dead: exec:",
		"project",
		"global",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered report is missing %q:\n%s", want, out)
		}
	}
}

// TestReportRowsTruncatesLongError keeps one verbose failure from wrecking the
// alignment of the whole block.
func TestReportRowsTruncatesLongError(t *testing.T) {
	reports := []ServerReport{{
		Name:  "loud",
		Scope: ScopeProject,
		State: StateFailed,
		Err:   strings.Repeat("boom ", 200),
	}}
	out := renderPlainRows(ReportRows(reports))
	if len(out) > 300 {
		t.Fatalf("a long error should be bounded, got %d bytes:\n%s", len(out), out)
	}
	if !strings.Contains(out, "…") {
		t.Fatalf("a truncated error should say so, got:\n%s", out)
	}
}

// TestToolRowsSkipsServersWithoutTools keeps /mcp tools free of empty lines.
func TestToolRowsSkipsServersWithoutTools(t *testing.T) {
	reports := []ServerReport{
		{Name: "dead", Scope: ScopeGlobal, State: StateFailed, Err: "nope"},
		{Name: "live", Scope: ScopeProject, State: StateConnected, ToolNames: []string{"mcp_live_echo", "mcp_live_big"}},
	}
	out := renderPlainRows(ToolRows(reports))
	if strings.Contains(out, "dead") {
		t.Errorf("a server with no tools should not be listed:\n%s", out)
	}
	if !strings.Contains(out, "mcp_live_echo, mcp_live_big") {
		t.Errorf("expected the wrapped tool names, got:\n%s", out)
	}
}
