package slash

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type Ctx struct {
	Cfg      *config.Config
	Session  *session.Session
	Perm     *permissions.Manager
	Agent    planModeAgent
	Client   commitClient
	Out      io.Writer
	Contexts *contextfiles.Store
	// Prompter asks interactive questions (e.g. Append vs Replace for
	// /context). It is optional: when nil, commands print the
	// non-interactive guidance instead. tui.UI satisfies it.
	Prompter InputPrompter
}

// InputPrompter reads one line of user input. It mirrors tui.UI.GetInput
// so the real UI can be passed without importing the tui package here.
type InputPrompter interface {
	GetInput(prompt string) (string, error)
}

type planModeAgent interface {
	EnterPlanMode()
	ExitPlanMode()
	InPlanMode() bool
	EnterReviewMode()
	ExitReviewMode()
	InReviewMode() bool
}

type commitClient interface {
	ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error)
}

func Dispatch(c *Ctx, line string) (bool, bool, error) {
	trimmed := strings.TrimSpace(line)

	// "bye" (case-insensitive) is a non-slash alias for /exit.
	if strings.EqualFold(trimmed, "bye") {
		if c.Cfg != nil && c.Cfg.Temporal {
			fmt.Fprintln(c.Out, "Temporal session discarded.")
			return true, true, nil
		}
		if c.Session != nil && c.Session.MessageCount() > 0 {
			if err := c.Session.Save(); err != nil {
				return true, false, err
			}
			fmt.Fprintf(c.Out, "Session saved (%s)\n", c.Session.ID())
		}
		return true, true, nil
	}

	if !strings.HasPrefix(trimmed, "/") {
		return false, false, nil
	}

	fields := strings.Fields(trimmed)
	cmd := fields[0]

	switch cmd {
	case "/exit", "/quit", "/q", "/bye":
		if c.Cfg != nil && c.Cfg.Temporal {
			fmt.Fprintln(c.Out, "Temporal session discarded.")
			return true, true, nil
		}
		if c.Session != nil && c.Session.MessageCount() > 0 {
			if err := c.Session.Save(); err != nil {
				return true, false, err
			}
			fmt.Fprintf(c.Out, "Session saved (%s)\n", c.Session.ID())
		}
		return true, true, nil
	case "/help", "/commands", "/cmds":
		printHelp(c)
		return true, false, nil
	case "/sessions":
		return true, false, runSessionsCommand(c, fields[1:])
	case "/session":
		return true, false, runSessionAlias(c, fields[1:])
	case "/resume":
		if len(fields) > 1 {
			fmt.Fprintln(c.Out, "Usage: /resume (takes no arguments; use /session <id> or /session last for other sessions)")
			return true, false, nil
		}
		return true, false, runResume(c)
	case "/new":
		if c.Cfg != nil && c.Cfg.Temporal {
			if c.Session != nil {
				c.Session.Clear()
			}
			fmt.Fprintf(c.Out, "Started a new temporal session (%s)\n", c.Session.ID())
			return true, false, nil
		}
		if c.Session != nil && c.Session.MessageCount() > 0 {
			if err := c.Session.Save(); err != nil {
				return true, false, err
			}
			c.Session.Clear()
			fmt.Fprintf(c.Out, "Session saved. Started a new session (%s)\n", c.Session.ID())
		} else {
			if c.Session != nil {
				c.Session.Clear()
			}
			fmt.Fprintf(c.Out, "Started a new session (%s)\n", c.Session.ID())
		}
		return true, false, nil
	case "/clear":
		return true, false, runClearCommand(c, fields[1:])
	case "/status":
		printStatus(c)
		return true, false, nil
	case "/sidecar":
		return true, false, runSidecarCommand(c, fields[1:])
	case "/jevstyle":
		return true, false, runJevstyleCommand(c, fields[1:])
	case "/save":
		if c.Cfg != nil && c.Cfg.Temporal {
			c.Cfg.Temporal = false
			if c.Session != nil && c.Session.MessageCount() > 0 {
				if err := c.Session.Save(); err != nil {
					return true, false, err
				}
				fmt.Fprintf(c.Out, "Session promoted to permanent and saved (%s)\n", c.Session.ID())
			} else {
				fmt.Fprintln(c.Out, "Session promoted to permanent (no messages to save yet)")
			}
			_ = config.SaveModelSettings(c.Cfg)
			return true, false, nil
		}
		hasMessages := c.Session != nil && c.Session.MessageCount() > 0
		if hasMessages {
			if err := c.Session.Save(); err != nil {
				return true, false, err
			}
		}
		if err := config.SaveModelSettings(c.Cfg); err != nil {
			return true, false, err
		}
		if hasMessages {
			fmt.Fprintf(c.Out, "Saved session (%s) and settings\n", c.Session.ID())
		} else {
			fmt.Fprintln(c.Out, "Saved settings")
		}
		return true, false, nil
	case "/hide-think":
		c.Cfg.OllamaHideThink = true
		fmt.Fprintln(c.Out, "Thinking blocks will be hidden from CLI output. Run /save to persist.")
		return true, false, nil
	case "/show-think":
		c.Cfg.OllamaHideThink = false
		fmt.Fprintln(c.Out, "Thinking blocks will be shown in CLI output. Run /save to persist.")
		return true, false, nil
	case "/yes", "/allow_all":
		enabled := true
		assisted := false
		if len(fields) > 1 {
			switch strings.ToLower(strings.TrimSpace(fields[1])) {
			case "assisted", "auto":
				assisted = true
			case "true", "on", "yes", "1", "enable":
				enabled = true
			case "false", "off", "no", "0", "disable":
				enabled = false
			default:
				fmt.Fprintln(c.Out, "Usage: /yes [true|false|assisted]")
				return true, false, nil
			}
		}
		if assisted {
			if !c.Cfg.JevstyleInUse() {
				fmt.Fprintln(c.Out, "JEV Style is not configured. Configure a model first with /jevstyle <model>.")
				return true, false, nil
			}
			c.Cfg.AssistedYes = true
			c.Cfg.YesMode = false
			if c.Perm != nil {
				c.Perm.SetAssistedMode(true)
				c.Perm.SetYesMode(false)
			}
			fmt.Fprintln(c.Out, "Assisted yes mode enabled (JEV Style will auto-approve safe commands).")
			return true, false, nil
		}
		c.Cfg.AssistedYes = false
		if c.Perm != nil {
			c.Perm.SetAssistedMode(false)
		}
		setYesMode(c, enabled)
		return true, false, nil
	case "/no":
		c.Cfg.AssistedYes = false
		if c.Perm != nil {
			c.Perm.SetAssistedMode(false)
		}
		setYesMode(c, false)
		return true, false, nil
	case "/compact":
		before := c.Session.TokenEstimate()
		if err := c.Session.Compact(context.Background(), true); err != nil {
			return true, false, err
		}
		after := c.Session.TokenEstimate()
		fmt.Fprintf(c.Out, "Compacted session tokens: %d -> %d\n", before, after)
		return true, false, nil
	case "/model", "/models":
		return true, false, runModelCommand(c, fields[1:])
	case "/think":
		return true, false, runThinkCommand(c, fields[1:])
	case "/tokens":
		printTokens(c)
		return true, false, nil
	case "/commit":
		msg, out, err := runCommitFlow(c)
		if err != nil {
			return true, false, err
		}
		if msg != "" {
			fmt.Fprintf(c.Out, "Committed: %s\n", msg)
		}
		if strings.TrimSpace(out) != "" {
			fmt.Fprintln(c.Out, out)
		}
		return true, false, nil
	case "/plan":
		return true, false, runPlanCommand(c, fields[1:])
	case "/code":
		exitPlanMode(c, "[System Note] Returned to act mode via /code.")
		fmt.Fprintln(c.Out, "Code mode enabled. Plan mode is now off.")
		return true, false, nil
	case "/approve":
		exitPlanMode(c, "[System Note] Plan approved. Returning to act mode.")
		fmt.Fprintln(c.Out, "Plan approved. Act mode restored; you can continue in the same conversation.")
		return true, false, nil
	case "/review":
		return runReviewCommand(c, fields[1:])
	case "/context":
		return true, false, runContextCommand(c, trimmed)
	default:
		fmt.Fprintf(c.Out, "Unknown command: %s\n", cmd)
		return true, false, nil
	}
}
