package slash

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// runClearCommand implements the multi-use /clear command:
//
//	/clear           Show the available clear options.
//	/clear session   Discard the current session WITHOUT saving it and start
//	                 a new one (like /new, but nothing is archived).
//	/clear sessions  Delete ALL saved sessions (asks for Y/n confirmation).
//	/clear context   Unpin all persistent context files (same as /context clear).
func runClearCommand(c *Ctx, args []string) error {
	if len(args) == 0 {
		printClearOptions(c)
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(args[0])) {
	case "session":
		return runClearSession(c)
	case "sessions":
		return runClearSessions(c, args[1:])
	case "context":
		return runContextCommand(c, "/context clear")
	default:
		fmt.Fprintf(c.Out, "Unknown clear target %q.\n", args[0])
		printClearOptions(c)
		return nil
	}
}

func printClearOptions(c *Ctx) {
	fmt.Fprintln(c.Out, "Clear options:")
	fmt.Fprintln(c.Out, "  /clear session   Discard the current session without saving it and start a new one")
	fmt.Fprintln(c.Out, "  /clear sessions  Delete ALL saved sessions (asks for confirmation)")
	fmt.Fprintln(c.Out, "  /clear context   Unpin all persistent context files for this session")
	fmt.Fprintln(c.Out, "  /new             Save the current session and start a new one")
}

// runClearSession drops the in-memory transcript without archiving it and
// starts a fresh session. A previously persisted file for the current id is
// removed as well so nothing is kept; a never-saved session simply resets.
func runClearSession(c *Ctx) error {
	oldID := c.Session.ID()
	if err := session.DeleteSession(c.Cfg, oldID); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("discard current session: %w", err)
	}
	c.Session.Clear()
	// Clear() keeps pinned context paths by design (they are dropped only
	// via /clear context). Persist the fresh id so the sidecar stays in sync.
	if err := c.Session.Save(); err != nil {
		return fmt.Errorf("save fresh session: %w", err)
	}
	fmt.Fprintf(c.Out, "Discarded current session without saving. Started a new session (%s)\n", c.Session.ID())
	return nil
}

// runClearSessions deletes every saved session after an explicit Y/n
// confirmation. Pass --yes to confirm non-interactively.
func runClearSessions(c *Ctx, args []string) error {
	confirmed := false
	for _, a := range args {
		switch strings.ToLower(strings.TrimSpace(a)) {
		case "--yes", "-y", "--force", "-f":
			confirmed = true
		}
	}
	if !confirmed {
		if c.Prompter == nil {
			fmt.Fprintln(c.Out, "Confirmation required: re-run as /clear sessions --yes to delete ALL saved sessions.")
			return nil
		}
		answer, err := c.Prompter.GetInput("Delete ALL saved sessions? This cannot be undone. [y/N]: ")
		if err != nil {
			fmt.Fprintln(c.Out, "Cancelled. No sessions deleted.")
			return nil
		}
		switch strings.ToLower(strings.TrimSpace(answer)) {
		case "y", "yes":
			confirmed = true
		default:
			fmt.Fprintln(c.Out, "Cancelled. No sessions deleted.")
			return nil
		}
	}
	return runSessionsDelete(c, []string{"--all"})
}
