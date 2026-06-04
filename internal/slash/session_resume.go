package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runResume(c *Ctx, id string) error {
	if c.Session.MessageCount() > 0 {
		if err := c.Session.Save(); err != nil {
			return fmt.Errorf("save current session before resume: %w", err)
		}
	}
	id = strings.TrimSpace(id)
	if id == "" {
		ok, err := c.Session.LoadByProject()
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(c.Out, "No previous session found for the current project path. Use /sessions to list, or /resume <id>.")
			return nil
		}
		printResumeContext(c, true)
		return nil
	}
	resolvedID, err := resolveSessionID(c, id)
	if err != nil {
		return err
	}
	if err := c.Session.Load(resolvedID); err != nil {
		return fmt.Errorf("load session %q: %w", resolvedID, err)
	}
	printResumeContext(c, false)
	return nil
}

func printResumeContext(c *Ctx, byProject bool) {
	st := tui.NewStyle(c.Out)
	if byProject {
		fmt.Fprintf(c.Out, "%s %s %s\n",
			st.BoldGreen("Resumed project session"),
			st.Cyan(c.Session.ID()),
			st.Dim(fmt.Sprintf("(%d messages)", c.Session.MessageCount())),
		)
	} else {
		fmt.Fprintf(c.Out, "%s %s %s\n",
			st.BoldGreen("Resumed session"),
			st.Cyan(c.Session.ID()),
			st.Dim(fmt.Sprintf("(%d messages)", c.Session.MessageCount())),
		)
	}
	msgs := c.Session.MessagesReadOnly()
	if len(msgs) == 0 {
		return
	}
	last := lastAssistantResponse(msgs)
	if strings.TrimSpace(last) != "" {
		fmt.Fprintln(c.Out, st.BoldYellow("Last assistant response"))
		fmt.Fprintln(c.Out, trimForDisplay(last, 1500))
	}
}
