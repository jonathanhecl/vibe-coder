package slash

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/mcp"
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
	// Models lists the models installed on the host so /models and /setmodel
	// can offer a numbered picker. Optional: when nil the pickers fall back to
	// the capability maps cached at startup.
	Models ModelLister
	// DefaultLister lists models from the default provider. When set, it
	// overrides Models for the default provider picker so /models shows the
	// provider's own capability tags even when the main model lives elsewhere.
	DefaultLister ModelLister
	// Prompter asks interactive questions (e.g. Append vs Replace for
	// /context). It is optional: when nil, commands print the
	// non-interactive guidance instead. tui.UI satisfies it.
	Prompter InputPrompter
	// MCP is the session's MCP runtime, reported by /mcp. Optional: when nil,
	// the command reports that no server is configured.
	MCP MCPReporter
	// LSP is the session's language server runtime, reported by /status and
	// used by the Diagnostics tool. Optional: when nil the row reports that no
	// server resolved for this workspace.
	LSP LSPReporter
}

// MCPReporter exposes the state of the configured MCP servers. *mcp.Manager
// satisfies it.
type MCPReporter interface {
	Reports() []mcp.ServerReport
}

// LSPReporter exposes the language servers resolved for the workspace. It is
// kept to the one method the rows need, so *lsp.Manager satisfies it without
// this package depending on the protocol client.
type LSPReporter interface {
	Resolved() map[string]string
}

// ModelLister lists the installed models. ollama.Client satisfies it.
type ModelLister interface {
	Tags(ctx context.Context) ([]ollama.Model, error)
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
		// Trailing form ("what do you see? /image") is handled in repl.go.
		if f := strings.Fields(trimmed); len(f) > 1 {
			if last := strings.ToLower(f[len(f)-1]); last == "/image" || last == "/paste" {
				return true, false, nil
			}
		}
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
	case "/redo":
		return true, false, runRedoCommand(c)
	case "/new":
		if c.Cfg != nil && c.Cfg.Temporal {
			if c.Session != nil {
				c.Session.Clear()
			}
			fmt.Fprintf(c.Out, "Started a new temporal session (%s)\n", c.Session.ID())
			return true, false, nil
		}
		if c.Cfg != nil && c.Cfg.Isolated {
			if c.Session != nil {
				if err := c.Session.ClearIsolated(); err != nil {
					return true, false, err
				}
			}
			fmt.Fprintf(c.Out, "Started a new isolated session (%s)\n", c.Session.ID())
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
	case "/status", "/info":
		printStatus(c)
		return true, false, nil
	case "/stats":
		printStats(c)
		return true, false, nil
	case "/skills":
		return true, false, runSkillsCommand(c, fields[1:])
	case "/mcp":
		return true, false, runMCPCommand(c, fields[1:])
	case "/sidecar":
		return true, false, runSidecarCommand(c, fields[1:])
	case "/decision":
		return true, false, runDecisionCommand(c, fields[1:])
	case "/save":
		return true, false, runSaveCommand(c)
	case "/promote", "/keep":
		return true, false, runPromoteCommand(c)
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
			if !c.Cfg.DecisionInUse() {
				fmt.Fprintln(c.Out, "A decision model is not configured. Bind one first with /setmodel decision <number|name>.")
				return true, false, nil
			}
			c.Cfg.AssistedYes = true
			c.Cfg.YesMode = false
			if c.Perm != nil {
				c.Perm.SetAssistedMode(true)
				c.Perm.SetYesMode(false)
			}
			fmt.Fprintln(c.Out, "Assisted yes mode enabled (the decision model will auto-approve safe commands).")
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
	case "/offline":
		return true, false, runOfflineCommand(c, fields[1:])
	case "/compact":
		before := c.Session.TokenEstimate()
		if err := c.Session.Compact(context.Background(), true); err != nil {
			return true, false, err
		}
		after := c.Session.TokenEstimate()
		fmt.Fprintf(c.Out, "Compacted session tokens: %d -> %d\n", before, after)
		return true, false, nil
	case "/models":
		return true, false, runModelsCommand(c, fields[1:])
	case "/providers":
		return true, false, runProvidersCommand(c, fields[1:])
	case "/provider":
		return true, false, runProviderCommand(c, fields[1:])
	case "/setmodel":
		return true, false, runSetModelCommand(c, fields[1:])
	case "/think":
		return true, false, runThinkCommand(c, fields[1:])
	case "/settimeout":
		return true, false, runSetTimeoutCommand(c, fields[1:])
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
	case "/goal":
		return true, false, runGoalCommand(c, fields[1:])
	case "/mission":
		return true, false, runMissionCommand(c, fields[1:])
	case "/review":
		return runReviewCommand(c, fields[1:])
	case "/context":
		return true, false, runContextCommand(c, trimmed)
	case "/tasks", "/bg":
		return true, false, runTasksCommand(c, fields[1:])
	case "/paste", "/image":
		// Clipboard paste is handled in repl.go after Dispatch returns,
		// following the same pattern as /plan and /review.
		return true, false, nil
	default:
		fmt.Fprintf(c.Out, "Unknown command: %s\n", cmd)
		return true, false, nil
	}
}
