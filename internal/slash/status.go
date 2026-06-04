package slash

import (
	"fmt"
	"strings"
)

func printStatus(c *Ctx) {
	ctxPct := 0
	if c.Cfg.ContextWindow > 0 {
		ctxPct = min(100, (c.Session.MessageCount()*120)/c.Cfg.ContextWindow)
	}
	fmt.Fprintf(c.Out, "Model: %s\n", c.Cfg.Model)
	fmt.Fprintf(c.Out, "Context: %d%%\n", ctxPct)
	fmt.Fprintf(c.Out, "CWD: %s\n", c.Cfg.Cwd)
	fmt.Fprintf(c.Out, "Messages: %d\n", c.Session.MessageCount())
	fmt.Fprintf(c.Out, "Session: %s\n", c.Session.ID())
	fmt.Fprintf(c.Out, "Yes mode: %t\n", c.Cfg.YesMode)
	fmt.Fprintf(c.Out, "Sidecar model: %s\n", strings.TrimSpace(c.Cfg.SidecarModel))
	if c.Cfg.SidecarDisabled {
		fmt.Fprintln(c.Out, "Sidecar: off (SIDECAR_DISABLED in config)")
	} else if c.Cfg.SidecarSkipSession {
		fmt.Fprintln(c.Out, "Sidecar: off for this session (/sidecar on)")
	} else if c.Cfg.SidecarInUse() {
		fmt.Fprintln(c.Out, "Sidecar: on")
	} else {
		fmt.Fprintln(c.Out, "Sidecar: off (no SIDECAR_MODEL)")
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
