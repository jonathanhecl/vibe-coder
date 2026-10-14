package main

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/version"
)

// bannerWordmark is the styled startup logo. It is only emitted when ANSI
// styling is available, so piped or NO_COLOR output stays plain and greppable.
const bannerWordmark = `  ██╗   ██╗██╗██████╗ ███████╗
  ██║   ██║██║██╔══██╗██╔════╝
  ██║   ██║██║██████╔╝█████╗
  ╚██╗ ██╔╝██║██╔══██╗██╔══╝
   ╚████╔╝ ██║██████╔╝███████╗
    ╚═══╝  ╚═╝╚═════╝ ╚══════╝`

// bannerRows returns the runtime facts surfaced at startup: the session id, one
// line per model role, and the mode flags. Roles keep the "Rol [provider] model"
// shape and show their fallback while they stay unset. Endpoints live in
// /providers, so they are not repeated here.
func bannerRows(cfg *config.Config, sessionID string, resumed bool) []tui.RoleRow {
	rows := make([]tui.RoleRow, 0, len(config.AllRoles)+2)
	rows = append(rows, sessionBannerRow(cfg, sessionID, resumed))
	rows = append(rows, withDecisionAssistedHint(tui.RoleRows(cfg), cfg)...)
	if cfg != nil && cfg.Offline {
		rows = append(rows, tui.RoleRow{
			Label:  "Offline",
			Plain:  "enabled (network blocked)",
			Render: func(st tui.Style) string { return st.Yellow("enabled (network blocked)") },
		})
	}
	return rows
}

// withDecisionAssistedHint adds the assisted state to the Decision row. The
// startup banner carries it because it has no Assisted row of its own; /status
// reports it on its own row instead.
func withDecisionAssistedHint(rows []tui.RoleRow, cfg *config.Config) []tui.RoleRow {
	if cfg == nil || !cfg.DecisionInUse() {
		return rows
	}
	for i := range rows {
		if rows[i].Label != config.RoleTitle(config.RoleDecision) {
			continue
		}
		base := rows[i].Render
		if cfg.AssistedYes {
			rows[i].Plain += " (assisted enabled)"
			rows[i].Render = func(st tui.Style) string {
				return base(st) + " " + st.Green("(assisted enabled)")
			}
		} else {
			rows[i].Plain += " (run '/yes assisted' to enable)"
			rows[i].Render = func(st tui.Style) string {
				return base(st) + " " + st.BrightWhite("(run '/yes assisted' to enable)")
			}
		}
	}
	return rows
}

// sessionBannerRow shows the session id plus its temporal/isolated/resumed tag.
func sessionBannerRow(cfg *config.Config, sessionID string, resumed bool) tui.RoleRow {
	white := func(st tui.Style) string { return st.BrightWhite(sessionID) }
	switch {
	case cfg != nil && cfg.Temporal:
		return tui.RoleRow{Label: "Session", Plain: sessionID + " (temporal)", Render: func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Red("(temporal)")
		}}
	case cfg != nil && cfg.Isolated:
		return tui.RoleRow{Label: "Session", Plain: sessionID + " (isolated)", Render: func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Yellow("(isolated)")
		}}
	case resumed:
		return tui.RoleRow{Label: "Session", Plain: sessionID + " (resumed)", Render: func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Cyan("(resumed)")
		}}
	default:
		return tui.RoleRow{Label: "Session", Plain: sessionID, Render: white}
	}
}

// startupBanner renders the startup header. The plain path keeps a line-based
// "Label: value" form for logs and pipes; the styled path draws the wordmark and
// an aligned, colored layout.
func startupBanner(cfg *config.Config, sessionID string, resumed bool, style tui.Style) string {
	rows := bannerRows(cfg, sessionID, resumed)
	if !style.Enabled() {
		var b strings.Builder
		fmt.Fprintf(&b, "vibe %s\n", version.Value)
		for _, r := range rows {
			if r.Label == "Session" {
				fmt.Fprintf(&b, "Session started: %s\n", r.Plain)
				continue
			}
			fmt.Fprintf(&b, "%s: %s\n", r.Label, r.Plain)
		}
		return b.String()
	}

	labelWidth := 0
	for _, r := range rows {
		if len(r.Label) > labelWidth {
			labelWidth = len(r.Label)
		}
	}

	var b strings.Builder
	b.WriteString("\n")
	b.WriteString(style.Lime(bannerWordmark))
	b.WriteString("\n\n")
	b.WriteString("  ")
	b.WriteString(style.DimGreen("local-first coding agent for Ollama"))
	b.WriteString("  ")
	b.WriteString(style.Gray("· " + version.Value))
	b.WriteString("\n  ")
	b.WriteString(style.Gray(strings.Repeat("─", 46)))
	b.WriteString("\n")
	for _, r := range rows {
		value := style.BrightWhite(r.Plain)
		if r.Render != nil {
			value = r.Render(style)
		}
		b.WriteString("  ")
		b.WriteString(style.BoldCyan(fmt.Sprintf("%-*s", labelWidth, r.Label)))
		b.WriteString(" ")
		b.WriteString(value)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	return b.String()
}
