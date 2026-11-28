package slash

import (
	"fmt"
	"strings"
)

// runAutoContinueCommand implements /autocontinue: keep starting a
// continuation turn while the previous one stopped with durable work left,
// instead of waiting for the user to press Enter after every turn.
//
//	/autocontinue            show the current state
//	/autocontinue on|off     set it for this session
//
// It is session-only. The loop lives in the REPL, not in the agent, because
// only the REPL knows whether a stop is continuable (pending TODOs, an active
// goal, an interrupted checkpoint) or needs the user.
func runAutoContinueCommand(c *Ctx, args []string) error {
	if c == nil || c.Cfg == nil {
		return fmt.Errorf("/autocontinue needs a loaded configuration")
	}
	arg := ""
	if len(args) > 0 {
		arg = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch arg {
	case "", "status":
		printAutoContinueStatus(c)
	case "on", "true", "yes", "enable", "enabled":
		c.Cfg.AutoContinue = true
		fmt.Fprintln(c.Out, "Auto-continue on: after a turn that stops with pending work, the agent keeps going by itself.")
		fmt.Fprintln(c.Out, "It still stops when the work is done, when it needs your input, when a turn fails, and on Ctrl+C.")
		fmt.Fprintln(c.Out, "Session only (not persisted): /autocontinue off turns it off.")
	case "off", "false", "no", "disable", "disabled":
		c.Cfg.AutoContinue = false
		fmt.Fprintln(c.Out, "Auto-continue off.")
	default:
		fmt.Fprintf(c.Out, "Invalid value %q: use /autocontinue on|off\n", arg)
	}
	return nil
}

func printAutoContinueStatus(c *Ctx) {
	state := "off"
	if c.Cfg.AutoContinue {
		state = "on"
	}
	fmt.Fprintf(c.Out, "Auto-continue: %s\n", state)
	fmt.Fprintln(c.Out, "Usage: /autocontinue on|off — after a turn stops with pending TODOs, an active goal or an interrupted checkpoint, start the next turn automatically (session only).")
}
