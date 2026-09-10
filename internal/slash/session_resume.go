package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runResume(c *Ctx) error {
	if c.Session.MessageCount() > 0 {
		if err := c.Session.Save(); err != nil {
			return fmt.Errorf("save current session before resume: %w", err)
		}
	}
	ok, err := c.Session.LoadByProject()
	if err != nil {
		return err
	}
	if !ok {
		fmt.Fprintln(c.Out, "No previous session found for the current project path. Use /sessions to list, or /session last.")
		return nil
	}
	printResumeContext(c, true)
	restorePinnedContexts(c)
	return nil
}

// loadSessionByID resolves an id (or unique prefix, with paste-block
// tolerance) and swaps to it. Used by /session <id>.
func loadSessionByID(c *Ctx, raw string) error {
	if c.Session.MessageCount() > 0 {
		if err := c.Session.Save(); err != nil {
			return fmt.Errorf("save current session before resume: %w", err)
		}
	}
	resolvedID, err := resolveSessionID(c, raw)
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
// is saved below, so without the exclusion "last" would reload itself).
func runResumeLast(c *Ctx) error {
	if c.Session.MessageCount() > 0 {
		if err := c.Session.Save(); err != nil {
			return fmt.Errorf("save current session before resume: %w", err)
		}
	}
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
