package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// runMCPCommand renders the state of the session's MCP servers. Without
// arguments it prints the same status block the startup banner prints; with
// "tools" it lists the wrapped tool names each server exposes.
func runMCPCommand(c *Ctx, args []string) error {
	st := tui.NewStyle(c.Out)

	var reports []mcp.ServerReport
	if c.MCP != nil {
		reports = c.MCP.Reports()
	}
	if len(reports) == 0 {
		fmt.Fprintln(c.Out, "No MCP servers configured.")
		fmt.Fprintln(c.Out, "Add one with: vibe mcp add [--global] [--env KEY=VALUE] <name> <command> [args...]")
		return nil
	}

	if len(args) > 0 {
		option := strings.ToLower(strings.TrimSpace(args[0]))
		if option != "tools" {
			fmt.Fprintf(c.Out, "Unknown /mcp option: %s\n", args[0])
			fmt.Fprintln(c.Out, "Usage: /mcp [tools]")
			return nil
		}
		tui.WriteBlock(c.Out, st, "mcp tools", mcp.ToolRows(reports))
		return nil
	}

	tui.WriteBlock(c.Out, st, "mcp", mcp.ReportRows(reports))
	return nil
}
