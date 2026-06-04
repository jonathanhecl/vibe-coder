package slash

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

var modelNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.:\-/]+$`)

func runSidecarCommand(c *Ctx, args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "off", "disable":
		c.Cfg.SidecarSkipSession = true
		fmt.Fprintln(c.Out, "Sidecar disabled for this session. Use /sidecar on to re-enable.")
	case "on", "enable":
		c.Cfg.SidecarSkipSession = false
		if c.Cfg.SidecarDisabled {
			fmt.Fprintln(c.Out, "Sidecar is still off in config (SIDECAR_DISABLED). Use /sidecar perm-on or edit config.env.")
		} else {
			fmt.Fprintln(c.Out, "Sidecar enabled for this session (if SIDECAR_MODEL is set).")
		}
	case "perm-off", "permanent-off", "config-off":
		c.Cfg.SidecarDisabled = true
		if err := config.SaveModelSettings(c.Cfg); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Saved SIDECAR_DISABLED=true to %s\n", c.Cfg.ConfigFile)
	case "perm-on", "permanent-on", "config-on":
		c.Cfg.SidecarDisabled = false
		if err := config.SaveModelSettings(c.Cfg); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Removed SIDECAR_DISABLED from %s (sidecar allowed when SIDECAR_MODEL is set).\n", c.Cfg.ConfigFile)
	case "status", "":
		printSidecarStatus(c)
	default:
		fmt.Fprintln(c.Out, "Usage: /sidecar on|off|status|perm-on|perm-off")
	}
	return nil
}

func printSidecarStatus(c *Ctx) {
	if c.Cfg.SidecarDisabled {
		fmt.Fprintln(c.Out, "Sidecar: permanently off in config (SIDECAR_DISABLED=true). Remove it or set SIDECAR_ENABLED=true, then /save if you use that flow.")
	} else if c.Cfg.SidecarSkipSession {
		fmt.Fprintln(c.Out, "Sidecar: off for this session. Model: "+strings.TrimSpace(c.Cfg.SidecarModel))
	} else if strings.TrimSpace(c.Cfg.SidecarModel) == "" {
		fmt.Fprintln(c.Out, "Sidecar: no model configured (SIDECAR_MODEL).")
	} else {
		fmt.Fprintln(c.Out, "Sidecar: on ("+strings.TrimSpace(c.Cfg.SidecarModel)+")")
	}
}

func runModelCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(c.Out, "Current model: %s\n", c.Cfg.Model)
		return nil
	}
	next := strings.TrimSpace(args[0])
	if !modelNameRe.MatchString(next) {
		fmt.Fprintln(c.Out, "Invalid model name format.")
		return nil
	}
	c.Cfg.Model = next
	fmt.Fprintf(c.Out, "Model set to: %s\n", c.Cfg.Model)
	return nil
}

func setYesMode(c *Ctx, enabled bool) {
	c.Cfg.YesMode = enabled
	if c.Perm != nil {
		c.Perm.SetYesMode(enabled)
	}
	if enabled {
		fmt.Fprintln(c.Out, "Yes mode enabled.")
	} else {
		fmt.Fprintln(c.Out, "Yes mode disabled.")
	}
}
