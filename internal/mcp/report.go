package mcp

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// ReportRows renders the server status through the shared row layout, so the
// startup block and `/mcp` describe the same state the same way. Servers are
// grouped by the scope their configuration came from.
func ReportRows(reports []ServerReport) []tui.Row {
	rows := make([]tui.Row, 0, len(reports))
	for _, r := range reports {
		plain, render := statusValue(r)
		rows = append(rows, tui.Row{
			Section: scopeSection(r.Scope),
			Label:   r.Name,
			Plain:   plain,
			Render:  render,
		})
	}
	return rows
}

// ToolRows renders the wrapped tool names of every server that exposed any,
// for `/mcp tools`.
func ToolRows(reports []ServerReport) []tui.Row {
	rows := make([]tui.Row, 0, len(reports))
	for _, r := range reports {
		if len(r.ToolNames) == 0 {
			continue
		}
		rows = append(rows, tui.Row{
			Section: scopeSection(r.Scope),
			Label:   r.Name,
			Plain:   strings.Join(r.ToolNames, ", "),
		})
	}
	return rows
}

// statusValue renders one server as "<state> · <detail>". A connected server
// counts its tools; a failed one carries the reason, because the failure is the
// signal and hiding it is the silent degradation this report exists to remove.
func statusValue(r ServerReport) (string, func(tui.Style) string) {
	if r.State != StateConnected {
		reason := shortError(r.Err)
		if reason == "" {
			reason = "no error reported"
		}
		plain := "failed · " + reason
		return plain, func(st tui.Style) string { return st.Red(plain) }
	}
	plain := "connected"
	switch n := len(r.ToolNames); n {
	case 0:
		plain += " · no tools"
	case 1:
		plain += " · 1 tool"
	default:
		plain += fmt.Sprintf(" · %d tools", n)
	}
	return plain, func(st tui.Style) string { return st.Green(plain) }
}

// shortError flattens an error into one bounded line, so a long server failure
// cannot wreck the alignment of the whole block.
func shortError(err string) string {
	msg := strings.Join(strings.Fields(err), " ")
	const maxRunes = 100
	runes := []rune(msg)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return msg
}

// scopeSection names the group a server is listed under.
func scopeSection(scope string) string {
	switch scope {
	case ScopeProject:
		return ScopeProject
	case ScopeGlobal:
		return ScopeGlobal
	default:
		return "other"
	}
}
