package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func uiModeLabel(mode string) string {
	switch mode {
	case "tui":
		return "tui (hybrid TUI)"
	case "rich":
		return "rich"
	default:
		return "plain (classic CLI)"
	}
}

func runUICommand(c *Ctx, args []string) error {
	if c == nil || c.Cfg == nil {
		return nil
	}
	current, ok := config.NormalizeUIMode(c.Cfg.UI)
	if !ok {
		current = "plain"
	}
	if len(args) == 0 {
		fmt.Fprintf(c.Out, "UI mode: %s\n", uiModeLabel(current))
		fmt.Fprintln(c.Out, "Usage: /ui <tui|cli|plain|rich>")
		return nil
	}

	target, ok := config.NormalizeUIMode(args[0])
	if !ok {
		fmt.Fprintf(c.Out, "Invalid UI mode %q. Usage: /ui <tui|cli|plain|rich>\n", args[0])
		return nil
	}

	prev := c.Cfg.UI
	c.Cfg.UI = target
	if current != target && c.OnUIChange != nil {
		if err := c.OnUIChange(target); err != nil {
			c.Cfg.UI = prev
			return fmt.Errorf("switch UI to %s: %w", target, err)
		}
	}

	savedNote := ""
	if strings.TrimSpace(c.Cfg.ConfigFile) != "" || strings.TrimSpace(c.Cfg.ConfigDir) != "" {
		if err := config.SaveModelSettings(c.Cfg); err != nil {
			fmt.Fprintf(c.Out, "warning: switched UI to %s, but failed to save settings: %v\n", uiModeLabel(target), err)
			return nil
		}
		savedNote = " and saved to settings"
	}

	if current == target {
		fmt.Fprintf(c.Out, "UI mode is already %s%s.\n", uiModeLabel(target), savedNote)
		return nil
	}
	fmt.Fprintf(c.Out, "Switched UI to %s%s.\n", uiModeLabel(target), savedNote)
	return nil
}
