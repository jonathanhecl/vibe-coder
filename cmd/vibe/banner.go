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

// bannerRows returns the runtime facts surfaced at startup: the session id, the
// model bound to each role, and the mode flags. Roles keep the
// "Rol [provider] model" shape and show their fallback while they stay unset.
// Endpoints live in /providers, so they are not repeated here.
func bannerRows(cfg *config.Config, sessionID string, resumed bool) []tui.Row {
	rows := make([]tui.Row, 0, len(config.AllRoles)+2)
	rows = append(rows, sessionBannerRow(cfg, sessionID, resumed))
	rows = append(rows, withDecisionAssistedHint(tui.RoleRows(cfg), cfg)...)
	if cfg != nil && cfg.Offline {
		rows = append(rows, tui.Row{
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
func withDecisionAssistedHint(rows []tui.Row, cfg *config.Config) []tui.Row {
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
func sessionBannerRow(cfg *config.Config, sessionID string, resumed bool) tui.Row {
	white := func(st tui.Style) string { return st.BrightWhite(sessionID) }
	switch {
	case cfg != nil && cfg.Temporal:
		return tui.Row{Label: "Session", PlainLabel: "Session started", Plain: sessionID + " (temporal)", Render: func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Red("(temporal)")
		}}
	case cfg != nil && cfg.Isolated:
		return tui.Row{Label: "Session", PlainLabel: "Session started", Plain: sessionID + " (isolated)", Render: func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Yellow("(isolated)")
		}}
	case resumed:
		return tui.Row{Label: "Session", PlainLabel: "Session started", Plain: sessionID + " (resumed)", Render: func(st tui.Style) string {
			return st.BrightWhite(sessionID) + "    " + st.Cyan("(resumed)")
		}}
	default:
		return tui.Row{Label: "Session", PlainLabel: "Session started", Plain: sessionID, Render: white}
	}
}

// startupBanner renders the startup header. The plain path keeps a line-based
// "Label: value" form for logs and pipes; the styled path draws the wordmark and
// an aligned, colored layout. Both share tui.WriteRows with /status.
func startupBanner(cfg *config.Config, sessionID string, resumed bool, style tui.Style) string {
	rows := bannerRows(cfg, sessionID, resumed)
	var b strings.Builder
	if !style.Enabled() {
		fmt.Fprintf(&b, "vibe %s\n", version.Value)
		tui.WriteRows(&b, style, rows)
		return b.String()
	}

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
	tui.WriteRows(&b, style, rows)
	b.WriteString("\n")
	return b.String()
}
