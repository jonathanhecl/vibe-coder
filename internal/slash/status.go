package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// missionReporter is the optional slice of the agent runtime that exposes the
// agent-managed mission. Kept local so older fakes keep compiling.
type missionReporter interface {
	MissionSnapshot() tools.Mission
}

// statusRow is one label/value pair. Plain output renders "Label: value"; the
// styled output renders the banner-like aligned layout, using Short as the
// label and Render (when set) for a colored value.
type statusRow struct {
	Label  string
	Short  string
	Plain  string
	Render func(tui.Style) string
}

func printStatus(c *Ctx) {
	writeStatus(c, tui.NewStyle(c.Out))
}

// writeStatus renders the current configuration. Colors and layout mirror the
// startup banner; plain output keeps the line-based "Label: value" form for
// logs and pipes. /info and /stats are aliases.
func writeStatus(c *Ctx, st tui.Style) {
	rows := statusRows(c)
	if len(rows) == 0 {
		return
	}
	if !st.Enabled() {
		for _, r := range rows {
			fmt.Fprintf(c.Out, "%s: %s\n", r.Label, r.Plain)
		}
		return
	}

	labelWidth := 0
	for _, r := range rows {
		if l := len(r.short()); l > labelWidth {
			labelWidth = l
		}
	}
	fmt.Fprintln(c.Out)
	fmt.Fprintf(c.Out, "  %s %s\n", st.BoldBrightGreen("vibe"), st.Gray("status"))
	fmt.Fprintf(c.Out, "  %s\n", st.Gray(strings.Repeat("─", 46)))
	for _, r := range rows {
		value := r.Plain
		if r.Render != nil {
			value = r.Render(st)
		} else {
			value = st.BrightWhite(r.Plain)
		}
		label := r.short()
		fmt.Fprintf(c.Out, "  %s%s  %s\n", st.BoldCyan(label), strings.Repeat(" ", labelWidth-len(label)), value)
	}
}

func (r statusRow) short() string {
	if strings.TrimSpace(r.Short) != "" {
		return r.Short
	}
	return r.Label
}

func statusRows(c *Ctx) []statusRow {
	rows := make([]statusRow, 0, 16)

	sessionID := ""
	if c.Session != nil {
		sessionID = c.Session.ID()
	}
	sessionPlain := sessionID
	sessionRender := func(st tui.Style) string { return st.BrightWhite(sessionID) }
	switch {
	case c.Cfg.Temporal:
		sessionPlain += "    (temporal)"
		sessionRender = func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Red("(temporal)")
		}
	case c.Cfg.Isolated:
		sessionPlain += "    (isolated)"
		sessionRender = func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Yellow("(isolated)")
		}
	}
	rows = append(rows, statusRow{Label: "Session", Plain: sessionPlain, Render: sessionRender})
	rows = append(rows, statusRow{Label: "CWD", Plain: c.Cfg.Cwd})
	rows = append(rows, statusRow{Label: "Ollama", Plain: c.Cfg.OllamaHost})
	ui := strings.TrimSpace(c.Cfg.UI)
	if ui == "" {
		ui = "plain"
	}
	rows = append(rows, statusRow{Label: "UI", Plain: ui})

	rows = append(rows, statusRow{Label: "Model", Plain: c.Cfg.Model})
	rows = append(rows, statusRow{Label: "Vision", Plain: visionWord(c.Cfg.VisionKnown, c.Cfg.VisionAvailable)})
	rows = append(rows, statusRow{Label: "Thinking", Plain: thinkingStatus(c)})
	rows = append(rows, statusRow{
		Label: "Hide think",
		Plain: yesNo(c.Cfg.OllamaHideThink),
		Render: func(st tui.Style) string {
			if c.Cfg.OllamaHideThink {
				return st.Yellow("on")
			}
			return st.Gray("off")
		},
	})
	rows = append(rows, statusRow{Label: "Tools", Plain: toolsStatus(c)})
	rows = append(rows, statusRow{Label: "Sidecar", Plain: sidecarStatusValue(c)})
	if strings.TrimSpace(c.Cfg.JevstyleModel) != "" {
		model := strings.TrimSpace(c.Cfg.JevstyleModel)
		plain := model
		if c.Cfg.AssistedYes {
			plain += " (assisted enabled)"
		} else {
			plain += " (run '/yes assisted' to enable)"
		}
		rows = append(rows, statusRow{
			Label: "JEV Style model",
			Short: "Jevstyle",
			Plain: plain,
			Render: func(st tui.Style) string {
				if c.Cfg.AssistedYes {
					return st.BrightWhite(model) + " " + st.Green("(assisted enabled)")
				}
				return st.BrightWhite(model) + " " + st.Gray("(run '/yes assisted' to enable)")
			},
		})
	}

	rows = append(rows, statusRow{Label: "Context", Plain: fmt.Sprintf("%d%%", contextUsagePct(c))})
	messages := 0
	if c.Session != nil {
		messages = c.Session.MessageCount()
	}
	rows = append(rows, statusRow{Label: "Messages", Plain: fmt.Sprintf("%d", messages)})
	rows = append(rows, statusRow{
		Label: "Yes mode",
		Plain: yesModeValue(c),
		Render: func(st tui.Style) string {
			if c.Cfg.YesMode || c.Cfg.AssistedYes {
				return st.Green(yesModeValue(c))
			}
			return st.Gray(yesModeValue(c))
		},
	})
	rows = append(rows, statusRow{
		Label: "Plan mode",
		Plain: planModeValue(c),
		Render: func(st tui.Style) string {
			if planModeValue(c) != "off" {
				return st.Yellow(planModeValue(c))
			}
			return st.Gray("off")
		},
	})

	if c.Contexts != nil && c.Contexts.Has() {
		rows = append(rows, statusRow{
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
			rows = append(rows, statusRow{Label: "Mission", Plain: line})
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
	switch {
	case c.Cfg.AssistedYes:
		return "on (assisted)"
	case c.Cfg.YesMode:
		return "on"
	default:
		return "off"
	}
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

// sidecarStatusValue condenses the sidecar state into one line, matching the
// wording used by the startup banner.
func sidecarStatusValue(c *Ctx) string {
	cfg := c.Cfg
	switch {
	case cfg.SidecarDisabled:
		return "disabled (SIDECAR_DISABLED in config)"
	case cfg.SidecarSkipSession:
		if m := strings.TrimSpace(cfg.SidecarModel); m != "" {
			return m + " (off for this session)"
		}
		return "off for this session (no SIDECAR_MODEL)"
	case strings.TrimSpace(cfg.SidecarModel) != "":
		return strings.TrimSpace(cfg.SidecarModel)
	default:
		return "disabled (set SIDECAR_MODEL)"
	}
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
