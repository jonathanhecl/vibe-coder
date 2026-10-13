package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runOfflineCommand(c *Ctx, args []string) error {
	st := tui.NewStyle(c.Out)
	om := c.Perm

	sub := ""
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}

	target := false
	switch sub {
	case "status":
		current := false
		if c.Cfg != nil {
			current = c.Cfg.Offline
		} else if om != nil {
			current = om.OfflineMode()
		}
		if current {
			fmt.Fprintln(c.Out, st.Yellow("Offline mode is currently ON (network tools & external commands blocked)."))
		} else {
			fmt.Fprintln(c.Out, st.Dim("Offline mode is currently OFF (network access allowed)."))
		}
		return nil
	case "on", "true", "yes", "enable", "1":
		target = true
	case "off", "false", "no", "disable", "0":
		target = false
	case "":
		// Toggle when no argument is specified
		if c.Cfg != nil {
			target = !c.Cfg.Offline
		} else if om != nil {
			target = !om.OfflineMode()
		} else {
			target = true
		}
	default:
		fmt.Fprintln(c.Out, "Usage: /offline [on|off|status]")
		return nil
	}

	if c.Cfg != nil {
		c.Cfg.Offline = target
	}
	if om != nil {
		om.SetOfflineMode(target)
	}

	if target {
		if c.Session != nil {
			c.Session.AddSystemNote("[System Note] Offline mode enabled: internet and network tools are blocked.")
		}
		fmt.Fprintln(c.Out, st.Yellow("Offline mode enabled: network tools (WebSearch, WebFetch, HTTPRequest) and external shell commands (curl, wget) are blocked."))
		if c.Cfg != nil && c.Cfg.DecisionInUse() {
			fmt.Fprintln(c.Out, st.Dim("The decision model will evaluate shell commands for external network access."))
		}
	} else {
		if c.Session != nil {
			c.Session.AddSystemNote("[System Note] Offline mode disabled: network and internet access restored.")
		}
		fmt.Fprintln(c.Out, st.Green("Offline mode disabled: internet and network access restored."))
	}
	return nil
}
