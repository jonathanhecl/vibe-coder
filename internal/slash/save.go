package slash

import (
	"fmt"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// runSaveCommand persists the model/thinking settings changed by /model,
// /sidecar, /jevstyle, /think, and /hide-think so they apply on the next run.
// It also saves the current session when it is permanent; temporal sessions
// stay ephemeral and must be kept explicitly with /promote.
func runSaveCommand(c *Ctx) error {
	if c.Cfg == nil {
		fmt.Fprintln(c.Out, "No configuration to save.")
		return nil
	}
	if err := config.SaveModelSettings(c.Cfg); err != nil {
		return err
	}
	if c.Cfg.Temporal {
		fmt.Fprintln(c.Out, "Saved settings. Temporal session not saved; run /promote to keep this conversation.")
		return nil
	}
	hasMessages := c.Session != nil && c.Session.MessageCount() > 0
	if hasMessages {
		if err := c.Session.Save(); err != nil {
			return err
		}
		if c.Cfg.Isolated {
			fmt.Fprintf(c.Out, "Saved isolated session (%s) and settings\n", c.Session.ID())
		} else {
			fmt.Fprintf(c.Out, "Saved session (%s) and settings\n", c.Session.ID())
		}
		return nil
	}
	fmt.Fprintln(c.Out, "Saved settings")
	return nil
}

// runPromoteCommand keeps the conversation: a temporal session is promoted to
// a permanent, saved session. For permanent sessions it just saves the
// transcript, matching the historical /save session behavior.
func runPromoteCommand(c *Ctx) error {
	if c.Cfg == nil || !c.Cfg.Temporal {
		if c.Session != nil && c.Session.MessageCount() > 0 {
			if err := c.Session.Save(); err != nil {
				return err
			}
			fmt.Fprintf(c.Out, "Session saved (%s)\n", c.Session.ID())
			return nil
		}
		fmt.Fprintln(c.Out, "Session is already permanent (nothing to promote).")
		return nil
	}
	c.Cfg.Temporal = false
	if c.Session != nil && c.Session.MessageCount() > 0 {
		if err := c.Session.Save(); err != nil {
			return err
		}
		fmt.Fprintf(c.Out, "Temporal session promoted to permanent and saved (%s)\n", c.Session.ID())
		return nil
	}
	fmt.Fprintln(c.Out, "Temporal session promoted to permanent (no messages to save yet)")
	return nil
}
