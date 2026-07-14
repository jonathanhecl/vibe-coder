package slash

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type Ctx struct {
	Cfg     *config.Config
	Session *session.Session
	Perm    *permissions.Manager
	Agent   planModeAgent
	Client  commitClient
	Out     io.Writer
}

type planModeAgent interface {
	EnterPlanMode()
	ExitPlanMode()
	InPlanMode() bool
}

type commitClient interface {
	ChatSync(ctx context.Context, req ollama.ChatRequest) (ollama.ChatResponse, error)
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
		printHelp(c)
		return true, false, nil
	case "/sessions":
		return true, false, runSessionsCommand(c, fields[1:])
	case "/session":
		return true, false, runSessionAlias(c, fields[1:])
	case "/resume":
		var id string
		if len(fields) > 1 {
			id = strings.TrimSpace(fields[1])
		}
		return true, false, runResume(c, id)
	case "/clear":
		if err := c.Session.Save(); err != nil {
			return true, false, err
		}
		c.Session.Clear()
		fmt.Fprintf(c.Out, "Started a new session (%s)\n", c.Session.ID())
		return true, false, nil
	case "/status":
		printStatus(c)
		return true, false, nil
	case "/sidecar":
		return true, false, runSidecarCommand(c, fields[1:])
	case "/save":
		if err := c.Session.Save(); err != nil {
			return true, false, err
		}
		if err := config.SaveModelSettings(c.Cfg); err != nil {
			return true, false, err
		}
		fmt.Fprintf(c.Out, "Saved session (%s) and settings\n", c.Session.ID())
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
		if len(fields) > 1 {
			switch strings.ToLower(strings.TrimSpace(fields[1])) {
			case "true", "on", "yes", "1", "enable":
				enabled = true
			case "false", "off", "no", "0", "disable":
				enabled = false
			default:
				fmt.Fprintln(c.Out, "Usage: /allow_all true|false")
				return true, false, nil
			}
		}
		setYesMode(c, enabled)
		return true, false, nil
	case "/no":
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
	default:
		fmt.Fprintf(c.Out, "Unknown command: %s\n", cmd)
		return true, false, nil
	}
}
