package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runReviewCommand(c *Ctx, args []string) (bool, bool, error) {
	prompt := strings.TrimSpace(strings.Join(args, " "))
	if prompt == "" {
		fmt.Fprintln(c.Out, "Usage: /review <prompt>")
		fmt.Fprintln(c.Out, "Review mode lets you ask the model in read-only mode (no edits, no commands).")
		return true, false, nil
	}
	st := tui.NewStyle(c.Out)
	if c.Agent != nil {
		c.Agent.EnterReviewMode()
		c.Session.AddUser("[System Note] Review mode enabled for this prompt.")
	}
	fmt.Fprintln(c.Out, st.Yellow("Review mode enabled. The model will be restricted to read-only tools for this prompt."))
	return true, false, nil
}
