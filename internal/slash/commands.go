package slash

import (
	"context"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

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

var modelNameRe = regexp.MustCompile(`^[a-zA-Z0-9_.:\-/]+$`)

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
		fmt.Fprintln(c.Out, "Commands: /exit /quit /q /help /clear /status /save /yes /no /compact /model /tokens /commit /plan /approve /sidecar")
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
		fmt.Fprintf(c.Out, "Sidecar model: %s\n", strings.TrimSpace(c.Cfg.SidecarModel))
		if c.Cfg.SidecarDisabled {
			fmt.Fprintln(c.Out, "Sidecar: off (SIDECAR_DISABLED in config)")
		} else if c.Cfg.SidecarSkipSession {
			fmt.Fprintln(c.Out, "Sidecar: off for this session (/sidecar on)")
		} else if c.Cfg.SidecarInUse() {
			fmt.Fprintln(c.Out, "Sidecar: on")
		} else {
			fmt.Fprintln(c.Out, "Sidecar: off (no SIDECAR_MODEL)")
		}
		return true, false, nil
	case "/sidecar":
		sub := "status"
		if len(fields) > 1 {
			sub = strings.ToLower(strings.TrimSpace(fields[1]))
		}
		switch sub {
		case "off", "disable":
			c.Cfg.SidecarSkipSession = true
			fmt.Fprintln(c.Out, "Sidecar disabled for this session. Use /sidecar on to re-enable.")
		case "on", "enable":
			c.Cfg.SidecarSkipSession = false
			if c.Cfg.SidecarDisabled {
				fmt.Fprintln(c.Out, "Sidecar is still off in config (SIDECAR_DISABLED). Use /sidecar perm-on or edit config.env.")
			} else {
				fmt.Fprintln(c.Out, "Sidecar enabled for this session (if SIDECAR_MODEL is set).")
			}
		case "perm-off", "permanent-off", "config-off":
			c.Cfg.SidecarDisabled = true
			if err := config.SaveModelSettings(c.Cfg); err != nil {
				return true, false, err
			}
			fmt.Fprintf(c.Out, "Saved SIDECAR_DISABLED=true to %s\n", c.Cfg.ConfigFile)
		case "perm-on", "permanent-on", "config-on":
			c.Cfg.SidecarDisabled = false
			if err := config.SaveModelSettings(c.Cfg); err != nil {
				return true, false, err
			}
			fmt.Fprintf(c.Out, "Removed SIDECAR_DISABLED from %s (sidecar allowed when SIDECAR_MODEL is set).\n", c.Cfg.ConfigFile)
		case "status", "":
			if c.Cfg.SidecarDisabled {
				fmt.Fprintln(c.Out, "Sidecar: permanently off in config (SIDECAR_DISABLED=true). Remove it or set SIDECAR_ENABLED=true, then /save if you use that flow.")
			} else if c.Cfg.SidecarSkipSession {
				fmt.Fprintln(c.Out, "Sidecar: off for this session. Model: " + strings.TrimSpace(c.Cfg.SidecarModel))
			} else if strings.TrimSpace(c.Cfg.SidecarModel) == "" {
				fmt.Fprintln(c.Out, "Sidecar: no model configured (SIDECAR_MODEL).")
			} else {
				fmt.Fprintln(c.Out, "Sidecar: on (" + strings.TrimSpace(c.Cfg.SidecarModel) + ")")
			}
		default:
			fmt.Fprintln(c.Out, "Usage: /sidecar on|off|status|perm-on|perm-off")
		}
		return true, false, nil
	case "/save":
		if err := c.Session.Save(); err != nil {
			return true, false, err
		}
		fmt.Fprintf(c.Out, "Saved session (%s)\n", c.Session.ID())
		return true, false, nil
	case "/yes":
		c.Cfg.YesMode = true
		if c.Perm != nil {
			c.Perm.SetYesMode(true)
		}
		fmt.Fprintln(c.Out, "Yes mode enabled.")
		return true, false, nil
	case "/no":
		c.Cfg.YesMode = false
		if c.Perm != nil {
			c.Perm.SetYesMode(false)
		}
		fmt.Fprintln(c.Out, "Yes mode disabled.")
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
		if len(fields) == 1 {
			fmt.Fprintf(c.Out, "Current model: %s\n", c.Cfg.Model)
			return true, false, nil
		}
		next := strings.TrimSpace(fields[1])
		if !modelNameRe.MatchString(next) {
			fmt.Fprintln(c.Out, "Invalid model name format.")
			return true, false, nil
		}
		c.Cfg.Model = next
		fmt.Fprintf(c.Out, "Model set to: %s\n", c.Cfg.Model)
		return true, false, nil
	case "/tokens":
		tokens := c.Session.TokenEstimate()
		pct := 0
		if c.Cfg.ContextWindow > 0 {
			pct = min(100, (tokens*100)/c.Cfg.ContextWindow)
		}
		bar := renderTokenBar(pct, 30)
		fmt.Fprintf(c.Out, "Tokens: %d / %d (%d%%)\n%s\n", tokens, c.Cfg.ContextWindow, pct, bar)
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
		if c.Agent != nil {
			c.Agent.EnterPlanMode()
			c.Session.AddUser("[System Note] Plan mode enabled.")
		}
		fmt.Fprintln(c.Out, "Plan mode enabled. Writes are restricted to .vibe-coder/plans.")
		return true, false, nil
	case "/approve":
		if c.Agent != nil {
			c.Agent.ExitPlanMode()
			c.Session.AddUser("[System Note] Plan approved. Returning to act mode.")
		}
		fmt.Fprintln(c.Out, "Plan approved. Act mode restored.")
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

func renderTokenBar(pct, width int) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	filled := (pct * width) / 100
	if filled > width {
		filled = width
	}
	return strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
}

func runCommitFlow(c *Ctx) (string, string, error) {
	if _, err := runGit(c.Cfg.Cwd, "rev-parse", "--is-inside-work-tree"); err != nil {
		return "", "Not a git repository, skipping commit.", nil
	}
	diff, err := runGit(c.Cfg.Cwd, "diff", "--staged")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(diff) == "" {
		unstaged, err := runGit(c.Cfg.Cwd, "diff")
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(unstaged) == "" {
			return "", "No changes to commit.", nil
		}
		if _, err := runGit(c.Cfg.Cwd, "add", "-A"); err != nil {
			return "", "", err
		}
		diff, err = runGit(c.Cfg.Cwd, "diff", "--staged")
		if err != nil {
			return "", "", err
		}
	}

	msg := "chore: update project files"
	if c.Client != nil {
		promptDiff := diff
		if len(promptDiff) > 4096 {
			promptDiff = promptDiff[:4096]
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		resp, err := c.Client.ChatSync(ctx, ollama.ChatRequest{
			Model: c.Cfg.Model,
			Messages: []ollama.Message{
				{Role: "system", Content: "Return one concise conventional commit message only."},
				{Role: "user", Content: "Diff:\n" + promptDiff},
			},
			Stream: false,
		})
		if err == nil && strings.TrimSpace(resp.Content) != "" {
			msg = sanitizeCommitMessage(resp.Content)
		}
	}

	if _, err := runGit(c.Cfg.Cwd, "commit", "-m", msg); err != nil {
		return "", "", err
	}
	return msg, "", nil
}

func runGit(cwd string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = cwd
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s failed: %w\n%s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func sanitizeCommitMessage(raw string) string {
	line := strings.TrimSpace(strings.Split(raw, "\n")[0])
	line = strings.Trim(line, "`\"")
	if line == "" {
		return "chore: update project files"
	}
	if len(line) > 72 {
		line = line[:72]
	}
	return line
}
