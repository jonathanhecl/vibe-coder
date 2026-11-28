package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
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
	st := tui.NewStyle(c.Out)
	arg := ""
	if len(args) > 0 {
		arg = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch arg {
	case "", "status":
		printAutoContinueStatus(c, st)
	case "on", "true", "yes", "enable", "enabled":
		c.Cfg.AutoContinue = true
		fmt.Fprintln(c.Out, autoContinueStateLine(st, true))
		fmt.Fprintln(c.Out, st.Dim("After a turn that stops with pending work the agent keeps going by itself."))
		fmt.Fprintln(c.Out, st.Dim("It still stops when the work is done, when it needs your input, when a turn fails, and on Ctrl+C."))
		fmt.Fprintln(c.Out, st.Dim("Session only (not persisted): /autocontinue off turns it off."))
	case "off", "false", "no", "disable", "disabled":
		c.Cfg.AutoContinue = false
		fmt.Fprintln(c.Out, autoContinueStateLine(st, false))
		fmt.Fprintln(c.Out, st.Dim("The agent waits for Enter after every turn again."))
	default:
		fmt.Fprintf(c.Out, "Invalid value %q: use /autocontinue on|off\n", arg)
	}
	return nil
}

// autoContinueStateLine is the ON/OFF confirmation in the state's own color:
// green when it is on, red when it is off, so the answer to "/autocontinue on"
// is readable at a glance in the transcript.
func autoContinueStateLine(st tui.Style, on bool) string {
	if on {
		return st.Green("Auto-continue ON")
	}
	return st.Red("Auto-continue OFF")
}

func printAutoContinueStatus(c *Ctx, st tui.Style) {
	if c.Cfg.AutoContinue {
		fmt.Fprintln(c.Out, st.Green("Auto-continue: ON"))
	} else {
		fmt.Fprintln(c.Out, st.Red("Auto-continue: OFF"))
	}
	fmt.Fprintln(c.Out, st.Dim("Usage: /autocontinue on|off — after a turn stops with pending TODOs, an active goal or an interrupted checkpoint, start the next turn automatically (session only)."))
}
