package slash

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// missionReporter is the optional slice of the agent runtime that exposes the
// agent-managed mission. Kept local so older fakes keep compiling.
type missionReporter interface {
	MissionSnapshot() tools.Mission
}

// printStatus renders the status table with the style of the command output.
func printStatus(c *Ctx) {
	writeStatus(c, tui.NewStyle(c.Out))
}

// writeStatus renders the current configuration through the shared row layout,
// so /status and the startup banner describe the same configuration the same
// way. Colors and the aligned table appear only when styling is enabled; plain
// output keeps the line-based "Label: value" form for logs and pipes.
func writeStatus(c *Ctx, st tui.Style) {
	tui.WriteBlock(c.Out, st, "status", statusRows(c))
}

func statusRows(c *Ctx) []tui.Row {
	rows := make([]tui.Row, 0, 16)

	sessionID := ""
	if c.Session != nil {
		sessionID = c.Session.ID()
	}
	sessionPlain := sessionID
	sessionRender := func(st tui.Style) string { return st.BrightWhite(sessionID) }
	switch {
	case c.Cfg.Temporal:
		sessionPlain += " (temporal)"
		sessionRender = func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Red("(temporal)")
		}
	case c.Cfg.Isolated:
		sessionPlain += " (isolated)"
		sessionRender = func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Yellow("(isolated)")
		}
	}
	rows = append(rows, tui.Row{Label: "Session", Plain: sessionPlain, Render: sessionRender})
	rows = append(rows, tui.Row{Label: "CWD", Plain: c.Cfg.Cwd})
	ui := strings.TrimSpace(c.Cfg.UI)
	if ui == "" {
		ui = "plain"
	}
	rows = append(rows, tui.Row{Label: "UI", Plain: ui})

	// Model wiring comes from the same renderer as the startup banner, grouped
	// under the same "Models" section; provider endpoints stay in /providers
	// instead of being repeated here.
	rows = append(rows, tui.RoleRows(c.Cfg)...)

	rows = append(rows, tui.ThinkingRow(c.Cfg))
	rows = append(rows, tui.Row{
		Label: "Hide think",
		Plain: yesNo(c.Cfg.OllamaHideThink),
		Render: func(st tui.Style) string {
			if c.Cfg.OllamaHideThink {
				return st.Yellow("on")
			}
			return st.Gray("off")
		},
	})
	rows = append(rows, tui.Row{Label: "Tools", Plain: toolsStatus(c)})
	rows = append(rows, tui.Row{
		Label: "Language servers",
		Short: "LSP",
		Plain: lspStatusValue(c),
		Render: func(st tui.Style) string {
			if lspResolved(c) {
				return st.Green(lspStatusValue(c))
			}
			if !c.Cfg.LSP.Requested() {
				return st.Gray(lspStatusValue(c))
			}
			return st.Yellow(lspStatusValue(c))
		},
	})
	rows = append(rows, tui.Row{
		Label: "Assisted",
		Plain: assistedStatusValue(c),
		Render: func(st tui.Style) string {
			if assistedActive(c) {
				return st.Green(assistedStatusValue(c))
			}
			return st.Gray(assistedStatusValue(c))
		},
	})

	rows = append(rows, tui.Row{Label: "Context", Plain: fmt.Sprintf("%d%%", contextUsagePct(c))})
	messages := 0
	if c.Session != nil {
		messages = c.Session.MessageCount()
	}
	rows = append(rows, tui.Row{Label: "Messages", Plain: fmt.Sprintf("%d", messages)})
	rows = append(rows, tui.Row{
		Label: "Yes mode",
		Plain: yesModeValue(c),
		Render: func(st tui.Style) string {
			if c.Cfg.YesMode {
				return st.Green(yesModeValue(c))
			}
			return st.Gray(yesModeValue(c))
		},
	})
	rows = append(rows, tui.Row{
		Label: "Plan mode",
		Plain: planModeValue(c),
		Render: func(st tui.Style) string {
			if planModeValue(c) != "off" {
				return st.Yellow(planModeValue(c))
			}
			return st.Gray("off")
		},
	})
	rows = append(rows, tui.Row{
		Label: "Offline",
		Plain: offlineStatusValue(c),
		Render: func(st tui.Style) string {
			if offlineActive(c) {
				return st.Yellow("enabled (network blocked)")
			}
			return st.Gray("disabled")
		},
	})

	if c.Contexts != nil && c.Contexts.Has() {
		rows = append(rows, tui.Row{
			Label: "Pinned contexts",
			Short: "Contexts",
			Plain: fmt.Sprintf("%d: %s", c.Contexts.Count(), strings.Join(c.Contexts.SortedNames(), ", ")),
		})
	}
	if mr, ok := c.Agent.(missionReporter); ok {
		if m := mr.MissionSnapshot(); m.Status != "" {
			line := m.Status
			if m.Turns > 0 {
				line += fmt.Sprintf(" (turn %d)", m.Turns)
			}
			if goal := strings.TrimSpace(m.Goal); goal != "" {
				line += " — " + goal
			}
			rows = append(rows, tui.Row{Label: "Mission", Plain: line})
		}
	}
	return rows
}

func yesNo(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

func contextUsagePct(c *Ctx) int {
	if c.Cfg.ContextWindow <= 0 || c.Session == nil {
		return 0
	}
	return min(100, (c.Session.MessageCount()*120)/c.Cfg.ContextWindow)
}

func yesModeValue(c *Ctx) string {
	if c.Cfg.YesMode {
		return "on"
	}
	return "off"
}

// assistedActive reports the effective decision-model assisted state. It prefers the
// permissions manager, which can auto-disable assisted mode when a decision
// model is missing or errors, over the configured ASSISTED_YES value.
func assistedActive(c *Ctx) bool {
	if c.Perm != nil {
		return c.Perm.AssistedMode()
	}
	return c.Cfg.AssistedYes && c.Cfg.DecisionInUse()
}

func assistedStatusValue(c *Ctx) string {
	switch {
	case assistedActive(c):
		return "on (decision model)"
	case c.Cfg.DecisionInUse():
		return "off (run '/yes assisted' to enable)"
	default:
		return "off (no decision model)"
	}
}

// lspResolved reports whether a language server answered for this workspace.
func lspResolved(c *Ctx) bool {
	if c.LSP == nil {
		return false
	}
	return len(c.LSP.Resolved()) > 0
}

// lspStatusValue describes the language servers the session resolved, or why
// there are none to show: switched off, or nothing in this workspace to run.
func lspStatusValue(c *Ctx) string {
	if !c.Cfg.LSP.Requested() {
		return "off (LSP_ENABLED=off)"
	}
	if !lspResolved(c) {
		if c.Cfg.LSP.Demanded() {
			return "none for this workspace (LSP_ENABLED=on)"
		}
		return "none for this workspace"
	}
	resolved := c.LSP.Resolved()
	families := make([]string, 0, len(resolved))
	for family, command := range resolved {
		families = append(families, family+": "+command)
	}
	sort.Strings(families)
	value := strings.Join(families, ", ")
	if c.Cfg.LSP.AfterEdit {
		return fmt.Sprintf("%s (after edit, %dms)", value, c.Cfg.LSP.TimeoutMS)
	}
	return fmt.Sprintf("%s (manual, %dms)", value, c.Cfg.LSP.TimeoutMS)
}

func planModeValue(c *Ctx) string {
	switch {
	case c.Agent != nil && c.Agent.InReviewMode():
		return "review"
	case c.Agent != nil && c.Agent.InPlanMode():
		return "on"
	default:
		return "off"
	}
}

func offlineActive(c *Ctx) bool {
	if c.Perm != nil {
		return c.Perm.OfflineMode()
	}
	return c.Cfg != nil && c.Cfg.Offline
}

func offlineStatusValue(c *Ctx) string {
	if offlineActive(c) {
		return "enabled (network blocked)"
	}
	return "disabled"
}

func printTokens(c *Ctx) {
	tokens := c.Session.TokenEstimate()
	pct := 0
	if c.Cfg.ContextWindow > 0 {
		pct = min(100, (tokens*100)/c.Cfg.ContextWindow)
	}
	bar := renderTokenBar(pct, 30)
	fmt.Fprintf(c.Out, "Tokens: %d / %d (%d%%)\n%s\n", tokens, c.Cfg.ContextWindow, pct, bar)
}

func renderTokenBar(pct, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := (pct * width) / 100
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}
