package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
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
		restorePinnedContexts(c)
		return nil
	}
	if strings.EqualFold(id, "last") || strings.EqualFold(id, "latest") {
		return runResumeLast(c)
	}
	resolvedID, err := resolveSessionID(c, id)
	if err != nil {
		return err
	}
	if err := c.Session.Load(resolvedID); err != nil {
		return fmt.Errorf("load session %q: %w", resolvedID, err)
	}
	printResumeContext(c, false)
	restorePinnedContexts(c)
	return nil
}

// runResumeLast loads the most recently modified saved session, excluding
// the current one (ListSessions sorts newest first, and the current session
// was just saved above, so without the exclusion "last" would reload itself).
func runResumeLast(c *Ctx) error {
	infos, err := session.ListSessions(c.Cfg)
	if err != nil {
		return err
	}
	current := ""
	if c.Session != nil {
		current = c.Session.ID()
	}
	for _, info := range infos {
		if info.ID == current {
			continue
		}
		if err := c.Session.Load(info.ID); err != nil {
			return fmt.Errorf("load session %q: %w", info.ID, err)
		}
		printResumeContext(c, false)
		restorePinnedContexts(c)
		return nil
	}
	st := tui.NewStyle(c.Out)
	fmt.Fprintln(c.Out, st.Yellow("No other saved sessions found. Use /sessions to list."))
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
