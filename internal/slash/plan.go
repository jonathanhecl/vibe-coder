package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runPlanCommand(c *Ctx, args []string) error {
	modeArg := ""
	if len(args) > 0 {
		modeArg = strings.ToLower(strings.TrimSpace(args[0]))
	}
	st := tui.NewStyle(c.Out)
	if modeArg == "off" || modeArg == "exit" || modeArg == "cancel" {
		exitPlanMode(c, "[System Note] Plan mode disabled without approval. Returning to act mode.")
		fmt.Fprintln(c.Out, st.Yellow("Plan mode disabled without approval. Act mode restored."))
		return nil
	}
	if c.Agent != nil {
		c.Agent.EnterPlanMode()
		c.Session.AddUser("[System Note] Plan mode enabled.")
	}
	fmt.Fprintln(c.Out, st.Yellow("Plan mode enabled. Keep chatting to design/refine the plan; writes are restricted to .vibe-coder/plans."))
	fmt.Fprintln(c.Out,
		st.Yellow("When ready to execute implementation changes, run ")+
			st.BrightWhite("/approve")+
			st.Yellow(" to return to act mode."),
	)
	fmt.Fprintln(c.Out,
		st.Yellow("If you want to leave plan mode without approving, run ")+
			st.Green("/code")+
			st.Yellow("."),
	)
	return nil
}

func exitPlanMode(c *Ctx, note string) {
	if c.Agent == nil {
		return
	}
	c.Agent.ExitPlanMode()
	c.Session.AddUser(note)
}
