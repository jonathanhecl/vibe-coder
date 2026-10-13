package slash

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

var modelNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.:\-/]+$`)

// runSidecarCommand toggles the sidecar on and off. The sidecar model itself is
// bound with /setmodel sidecar <number|name>, so this command only carries the
// session and config state that /setmodel cannot express.
func runSidecarCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printSidecarStatus(c)
		printSidecarUsage(c)
		return nil
	}
	sub := strings.ToLower(strings.TrimSpace(args[0]))
	switch sub {
	case "on", "enable":
		c.Cfg.SidecarSkipSession = false
		if c.Cfg.SidecarDisabled {
			fmt.Fprintln(c.Out, "Sidecar is still off in config (SIDECAR_DISABLED). Use /sidecar perm-on or edit vibe-coder.env.")
		} else {
			fmt.Fprintln(c.Out, "Sidecar enabled for this session (when a sidecar model is bound).")
		}
	case "off", "disable":
		c.Cfg.SidecarSkipSession = true
		fmt.Fprintln(c.Out, "Sidecar disabled for this session. Use /sidecar on to re-enable.")
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
		fmt.Fprintf(c.Out, "Removed SIDECAR_DISABLED from %s (sidecar allowed when a sidecar model is bound).\n", c.Cfg.ConfigFile)
	case "status":
		printSidecarStatus(c)
	default:
		fmt.Fprintf(c.Out, "Unknown /sidecar action %q.\n", sub)
		printSidecarUsage(c)
	}
	return nil
}

func printSidecarUsage(c *Ctx) {
	fmt.Fprintln(c.Out, "Usage: /sidecar on|off|status|perm-on|perm-off")
	fmt.Fprintln(c.Out, "Bind the sidecar model with /setmodel sidecar <number|name> (see /models).")
}

// sidecarModelName reads the sidecar model from the role registry, falling back
// to the legacy flat field for contexts built without a registry.
func sidecarModelName(c *Ctx) string {
	if name := c.Cfg.RoleModelName(config.RoleSidecar); name != "" {
		return name
	}
	return strings.TrimSpace(c.Cfg.SidecarModel)
}

func printSidecarStatus(c *Ctx) {
	switch {
	case c.Cfg.SidecarDisabled:
		fmt.Fprintln(c.Out, "Sidecar: permanently off in config (SIDECAR_DISABLED=true). Use /sidecar perm-on to re-enable.")
	case c.Cfg.SidecarSkipSession:
		fmt.Fprintln(c.Out, "Sidecar: off for this session. Model: "+sidecarModelName(c))
	case sidecarModelName(c) == "":
		fmt.Fprintln(c.Out, "Sidecar: no model bound. Use /setmodel sidecar <number|name> (see /models).")
	default:
		fmt.Fprintln(c.Out, "Sidecar: on ("+sidecarModelName(c)+", vision: "+visionWord(c.Cfg.SidecarVisionKnown, c.Cfg.SidecarVisionAvailable)+")")
	}
}

// probeCapabilities asks /api/show for one model's capabilities and caches
// them in the per-model maps, so a role switch reports the truth for that exact
// tag instead of a stale snapshot (or a sibling tag). It is a no-op when the
// client cannot inspect models or the probe fails, leaving the cached values
// in place (unknown when absent).
func probeCapabilities(c *Ctx, name string) {
	if c == nil || c.Cfg == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	inspector, ok := c.Models.(interface {
		Show(context.Context, string) (ollama.Model, error)
	})
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	info, err := inspector.Show(ctx, name)
	if err != nil || !info.CapabilitiesKnown {
		return
	}
	key := strings.ToLower(name)
	if c.Cfg.VisionByModel == nil {
		c.Cfg.VisionByModel = map[string]bool{}
	}
	if c.Cfg.ThinkingByModel == nil {
		c.Cfg.ThinkingByModel = map[string]bool{}
	}
	if c.Cfg.ToolsByModel == nil {
		c.Cfg.ToolsByModel = map[string]bool{}
	}
	c.Cfg.VisionByModel[key] = info.SupportsVision()
	c.Cfg.ThinkingByModel[key] = info.SupportsThinking()
	c.Cfg.ToolsByModel[key] = info.SupportsTools()
}

func refreshToolsFlags(c *Ctx) {
	supported, known := ollama.LookupVision(c.Cfg.ToolsByModel, c.Cfg.Model)
	c.Cfg.ToolsSupported, c.Cfg.ToolsKnown = supported, known
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

// visionStatusValue reports who looks at images for the main model: the
// model's own capability, plus the model bound to the vision role when there
// is one.
func visionStatusValue(c *Ctx) string {
	value := visionWord(c.Cfg.VisionKnown, c.Cfg.VisionAvailable)
	if role := strings.TrimSpace(c.Cfg.RoleModelName(config.RoleVision)); role != "" {
		value += " (vision role: " + role + ")"
	}
	return value
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
