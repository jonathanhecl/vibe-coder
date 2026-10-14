package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runSessionsDelete(c *Ctx, args []string) error {
	st := tui.NewStyle(c.Out)
	if len(args) == 0 {
		fmt.Fprintln(c.Out, st.Yellow("Usage: /sessions delete <id> | --all"))
		return nil
	}
	target := strings.TrimSpace(args[0])
	if target == "--all" || target == "-a" || target == "all" {
		// An isolated run owns a single folder-local session; deleting every
		// saved session would reach into the user-level state directory.
		if c.Cfg != nil && c.Cfg.Isolated {
			return clearIsolatedSession(c, true)
		}
		removed, err := session.DeleteAllSessions(c.Cfg)
		if err != nil {
			return err
		}
		c.Session.Clear()
		fmt.Fprintf(c.Out, "%s %s\n",
			st.BoldRed("Deleted"),
			st.Bold(fmt.Sprintf("%d session(s)", removed)),
		)
		fmt.Fprintf(c.Out, "%s %s\n",
			st.Dim("Started a new in-memory session"),
			st.Cyan("("+c.Session.ID()+")"),
		)
		return nil
	}
	if c.Cfg != nil && c.Cfg.Isolated {
		if target != c.Session.ID() {
			fmt.Fprintf(c.Out, "%s %s\n",
				st.Yellow("Isolated mode only clears its own session"),
				st.Dim("("+target+" is not stored in this folder)"),
			)
			return nil
		}
		return clearIsolatedSession(c, false)
	}
	resolvedID, err := resolveSessionID(c, target)
	if err != nil {
		return err
	}
	if err := session.DeleteSession(c.Cfg, resolvedID); err != nil {
		return fmt.Errorf("delete session %q: %w", resolvedID, err)
	}
	if c.Session.ID() == resolvedID {
		c.Session.Clear()
		fmt.Fprintf(c.Out, "%s %s %s %s\n",
			st.BoldRed("Deleted"),
			st.Cyan(resolvedID),
			st.Dim("- started fresh in-memory session"),
			st.Cyan("("+c.Session.ID()+")"),
		)
		return nil
	}
	fmt.Fprintf(c.Out, "%s %s\n", st.BoldRed("Deleted"), st.Cyan(resolvedID))
	return nil
}

// clearIsolatedSession empties the folder-local isolated session, which is the
// only session an isolated run can delete. all reports the whole-session form.
func clearIsolatedSession(c *Ctx, all bool) error {
	st := tui.NewStyle(c.Out)
	id := c.Session.ID()
	if err := c.Session.ClearIsolated(); err != nil {
		return err
	}
	if all {
		fmt.Fprintf(c.Out, "%s %s\n",
			st.BoldRed("Deleted"),
			st.Bold("the isolated session"),
		)
		fmt.Fprintf(c.Out, "%s %s\n",
			st.Dim("Started a new in-memory session"),
			st.Cyan("("+c.Session.ID()+")"),
		)
		return nil
	}
	fmt.Fprintf(c.Out, "%s %s %s %s\n",
		st.BoldRed("Deleted"),
		st.Cyan(id),
		st.Dim("- started fresh in-memory session"),
		st.Cyan("("+c.Session.ID()+")"),
	)
	return nil
}
