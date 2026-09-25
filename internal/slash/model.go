package slash

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

var modelNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.:\-/]+$`)

func runSidecarCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printSidecarStatus(c)
		chosen, changed, err := c.pickModel(context.Background(), "sidecar", "Sidecar", c.Cfg.SidecarModel, true)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if chosen == "" {
			c.Cfg.SidecarSkipSession = true
			fmt.Fprintln(c.Out, "Sidecar disabled for this session. Run /sidecar perm-off to disable it permanently.")
			return nil
		}
		c.Cfg.SidecarModel = chosen
		c.Cfg.SidecarDisabled = false
		c.Cfg.SidecarSkipSession = false
		fmt.Fprintf(c.Out, "Sidecar model set to: %s. Run /save to persist.\n", chosen)
		return nil
	}
	raw := strings.TrimSpace(args[0])
	sub := strings.ToLower(raw)
	switch sub {
	case "off", "disable":
		c.Cfg.SidecarSkipSession = true
		fmt.Fprintln(c.Out, "Sidecar disabled for this session. Use /sidecar on to re-enable.")
	case "on", "enable":
		c.Cfg.SidecarSkipSession = false
		if c.Cfg.SidecarDisabled {
			fmt.Fprintln(c.Out, "Sidecar is still off in config (SIDECAR_DISABLED). Use /sidecar perm-on or edit vibe-coder.env.")
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
		resolved, ok := c.resolveModelArg(context.Background(), raw)
		if !ok {
			fmt.Fprintf(c.Out, "No model number %s in the installed list.\n", raw)
			return nil
		}
		if !modelNameRe.MatchString(resolved) {
			fmt.Fprintln(c.Out, "Usage: /sidecar [model] | on|off|status|perm-on|perm-off")
			return nil
		}
		c.Cfg.SidecarModel = resolved
		c.Cfg.SidecarDisabled = false
		c.Cfg.SidecarSkipSession = false
		fmt.Fprintf(c.Out, "Sidecar model set to: %s. Run /save to persist.\n", resolved)
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
		fmt.Fprintln(c.Out, "Sidecar: on ("+strings.TrimSpace(c.Cfg.SidecarModel)+", vision: "+visionWord(c.Cfg.SidecarVisionKnown, c.Cfg.SidecarVisionAvailable)+")")
	}
}

func runModelCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		fmt.Fprintf(c.Out, "Current model: %s\n", c.Cfg.Model)
		chosen, changed, err := c.pickModel(context.Background(), "model", "Model", c.Cfg.Model, false)
		if err != nil {
			return err
		}
		if changed {
			applyModelSwitch(c, chosen)
		}
		return nil
	}
	next := strings.TrimSpace(args[0])
	resolved, ok := c.resolveModelArg(context.Background(), next)
	if !ok {
		fmt.Fprintf(c.Out, "No model number %s in the installed list.\n", next)
		return nil
	}
	if !modelNameRe.MatchString(resolved) {
		fmt.Fprintln(c.Out, "Invalid model name format.")
		return nil
	}
	applyModelSwitch(c, resolved)
	return nil
}

// applyModelSwitch activates a model and refreshes the vision, thinking, and
// tool-calling flags for the session.
func applyModelSwitch(c *Ctx, next string) {
	c.Cfg.Model = next
	// Refresh vision support from the cached Tags map so the system prompt
	// and /status stay honest after a switch. Unknown models report unknown.
	available, known := ollama.LookupVision(c.Cfg.VisionByModel, c.Cfg.Model)
	c.Cfg.VisionAvailable, c.Cfg.VisionKnown = available, known
	refreshThinkingFlags(c)
	refreshToolsFlags(c)
	fmt.Fprintf(c.Out, "Model set to: %s (vision: %s, thinking: %s, tools: %s). Run /save to persist.\n",
		c.Cfg.Model, visionWord(known, available), thinkingWord(c), toolsWord(c))
}

func refreshToolsFlags(c *Ctx) {
	supported, known := ollama.LookupVision(c.Cfg.ToolsByModel, c.Cfg.Model)
	c.Cfg.ToolsSupported, c.Cfg.ToolsKnown = supported, known
}

func toolsWord(c *Ctx) string {
	switch {
	case c.Cfg.ToolsKnown && c.Cfg.ToolsSupported:
		return "native"
	case c.Cfg.ToolsKnown && !c.Cfg.ToolsSupported:
		return "xml"
	default:
		return "auto"
	}
}

func thinkingWord(c *Ctx) string {
	switch {
	case c.Cfg.ThinkingKnown && c.Cfg.ThinkingSupported:
		return "yes"
	case c.Cfg.ThinkingKnown && !c.Cfg.ThinkingSupported:
		return "no"
	default:
		return "unknown"
	}
}

func visionWord(known, available bool) string {
	switch {
	case known && available:
		return "yes"
	case known && !available:
		return "no"
	default:
		return "unknown"
	}
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
