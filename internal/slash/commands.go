package slash

import (
	"fmt"
	"io"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type Ctx struct {
	Cfg     *config.Config
	Session *session.Session
	Out     io.Writer
}

func Dispatch(c *Ctx, line string) (bool, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/") {
		return false, false, nil
	}

	fields := strings.Fields(trimmed)
	cmd := fields[0]

	switch cmd {
	case "/exit", "/quit", "/q":
		if err := c.Session.Save(); err != nil {
			return true, false, err
		}
		fmt.Fprintf(c.Out, "Session saved (%s)\n", c.Session.ID())
		return true, true, nil
	case "/help":
		fmt.Fprintln(c.Out, "Commands: /exit /quit /q /help /clear /status /save /yes /no")
		return true, false, nil
	case "/clear":
		if err := c.Session.Save(); err != nil {
			return true, false, err
		}
		c.Session.Clear()
		fmt.Fprintf(c.Out, "Started a new session (%s)\n", c.Session.ID())
		return true, false, nil
	case "/status":
		ctxPct := 0
		if c.Cfg.ContextWindow > 0 {
			// Lightweight estimate during MVP.
			ctxPct = min(100, (c.Session.MessageCount()*120)/c.Cfg.ContextWindow)
		}
		fmt.Fprintf(c.Out, "Model: %s\n", c.Cfg.Model)
		fmt.Fprintf(c.Out, "Context: %d%%\n", ctxPct)
		fmt.Fprintf(c.Out, "CWD: %s\n", c.Cfg.Cwd)
		fmt.Fprintf(c.Out, "Messages: %d\n", c.Session.MessageCount())
		fmt.Fprintf(c.Out, "Session: %s\n", c.Session.ID())
		fmt.Fprintf(c.Out, "Yes mode: %t\n", c.Cfg.YesMode)
		return true, false, nil
	case "/save":
		if err := c.Session.Save(); err != nil {
			return true, false, err
		}
		fmt.Fprintf(c.Out, "Saved session (%s)\n", c.Session.ID())
		return true, false, nil
	case "/yes":
		c.Cfg.YesMode = true
		fmt.Fprintln(c.Out, "Yes mode enabled.")
		return true, false, nil
	case "/no":
		c.Cfg.YesMode = false
		fmt.Fprintln(c.Out, "Yes mode disabled.")
		return true, false, nil
	default:
		fmt.Fprintf(c.Out, "Unknown command: %s\n", cmd)
		return true, false, nil
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
