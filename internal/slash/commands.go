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
	"github.com/jonathanhecl/vibe-coder/internal/tui"
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

// runSessionsCommand dispatches /sessions and its subcommands.
// Supported forms:
//   /sessions                    -> list (default)
//   /sessions list               -> list (explicit)
//   /sessions delete <id>        -> remove a single saved session
//   /sessions delete --all       -> wipe every saved session + index
//   /sessions rm <id>            -> alias for delete
func runSessionsCommand(c *Ctx, args []string) error {
	sub := "list"
	if len(args) > 0 {
		sub = strings.ToLower(strings.TrimSpace(args[0]))
	}
	switch sub {
	case "", "list", "ls":
		return runSessionsList(c)
	case "delete", "del", "rm", "remove":
		return runSessionsDelete(c, args[1:])
	default:
		fmt.Fprintln(c.Out, "Usage: /sessions [list | delete <id> | delete --all]")
		return nil
	}
}

// runSessionAlias supports "/session" as a shorthand UX command:
//   /session            -> /sessions list
//   /session <id>       -> /resume <id>
//   /session delete ... -> /sessions delete ...
func runSessionAlias(c *Ctx, args []string) error {
	if len(args) == 0 {
		return runSessionsList(c)
	}
	first := strings.ToLower(strings.TrimSpace(args[0]))
	switch first {
	case "list", "ls", "delete", "del", "rm", "remove":
		return runSessionsCommand(c, args)
	default:
		return runResume(c, args[0])
	}
}

// runSessionsList prints a compact, color-aware table of saved sessions
// (most recent first), highlighting the one mapped to the current cwd.
func runSessionsList(c *Ctx) error {
	infos, err := session.ListSessions(c.Cfg)
	if err != nil {
		return err
	}
	st := tui.NewStyle(c.Out)
	if len(infos) == 0 {
		fmt.Fprintf(c.Out, "%s %s\n",
			st.Yellow("No sessions found in"),
			st.Dim(c.Cfg.SessionsDir),
		)
		return nil
	}
	fmt.Fprintf(c.Out, "%s %s\n",
		st.BoldCyan("Sessions in"),
		st.Dim(c.Cfg.SessionsDir),
	)
	header := fmt.Sprintf("  %-2s %-32s %-16s %-5s  %s", "", "ID", "MODIFIED", "MSGS", "PREVIEW")
	fmt.Fprintln(c.Out, st.Dim(header))
	for _, info := range infos {
		marker := "  "
		if info.IsCurrentProject {
			marker = st.BoldGreen("*")
		}
		preview := info.Preview
		if preview == "" {
			preview = st.Dim("(no user message)")
		}
		row := fmt.Sprintf("  %s %-32s %-16s %5d  %s",
			marker,
			st.Cyan(info.ID),
			st.Gray(info.ModTime.Local().Format("2006-01-02 15:04")),
			info.MessageCount,
			preview,
		)
		fmt.Fprintln(c.Out, row)
	}
	fmt.Fprintln(c.Out, st.Dim("(* = current project path | use /resume <id>, /session <id>, or /sessions delete <id>)"))
	return nil
}

// runSessionsDelete handles "/sessions delete <id>" and
// "/sessions delete --all". When the deleted session is the one currently
// loaded in memory we also clear the live transcript so the REPL doesn't
// keep silently writing to a freshly-deleted file on the next /save.
func runSessionsDelete(c *Ctx, args []string) error {
	st := tui.NewStyle(c.Out)
	if len(args) == 0 {
		fmt.Fprintln(c.Out, st.Yellow("Usage: /sessions delete <id> | --all"))
		return nil
	}
	target := strings.TrimSpace(args[0])
	if target == "--all" || target == "-a" || target == "all" {
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

// printHelp renders a grouped, color-aware command reference. We keep the
// layout single-column so it works in narrow terminals; the colors and
// dim descriptions do the heavy lifting visually.
func printHelp(c *Ctx) {
	st := tui.NewStyle(c.Out)
	fmt.Fprintln(c.Out, st.BoldCyan("vibe-coder commands"))
	groups := []struct {
		title string
		items [][2]string
	}{
		{"Session", [][2]string{
			{"/save", "persist the current session to disk"},
			{"/clear", "save and start a brand new session"},
			{"/sessions", "list saved sessions (* = current project)"},
			{"/session <id>", "resume a specific session quickly"},
			{"/sessions delete <id>", "delete a specific session"},
			{"/sessions delete --all", "delete every saved session"},
			{"/resume", "resume the last session for this project path"},
			{"/resume <id>", "resume a specific session by id (or unique prefix)"},
			{"/compact", "force a sidecar-summarized compaction"},
			{"/tokens", "show token usage vs the context window"},
			{"/status", "model, cwd, session and sidecar status"},
		}},
		{"Model", [][2]string{
			{"/model", "show the active model"},
			{"/model <name>", "switch the active model for this run"},
			{"/sidecar on|off", "toggle the sidecar for this session"},
			{"/sidecar perm-on|perm-off", "persist sidecar state to config.env"},
			{"/sidecar status", "show current sidecar state"},
		}},
		{"Mode", [][2]string{
			{"/yes", "auto-approve subsequent permission prompts"},
			{"/no", "require manual approval (default)"},
			{"/plan", "enter plan mode (writes restricted to .vibe-coder/plans)"},
			{"/approve", "exit plan mode and resume act mode"},
		}},
		{"Git", [][2]string{
			{"/commit", "stage + commit current changes (LLM-suggested message)"},
		}},
		{"Misc", [][2]string{
			{"/help", "show this help"},
			{"/exit, /quit, /q", "save and exit"},
		}},
	}
	maxCmd := 0
	for _, g := range groups {
		for _, it := range g.items {
			if l := len(it[0]); l > maxCmd {
				maxCmd = l
			}
		}
	}
	for i, g := range groups {
		if i > 0 {
			fmt.Fprintln(c.Out)
		}
		fmt.Fprintln(c.Out, st.BoldYellow(g.title))
		for _, it := range g.items {
			pad := strings.Repeat(" ", maxCmd-len(it[0]))
			fmt.Fprintf(c.Out, "  %s%s  %s\n",
				st.Green(it[0]),
				pad,
				st.Dim(it[1]),
			)
		}
	}
}

// runResume loads a previously saved session into the live REPL state.
// With no id, it falls back to the project-index entry for the current
// cwd. The caller's current in-memory transcript is persisted first so we
// never silently drop unsaved messages when swapping.
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
	msgs := c.Session.Messages()
	if len(msgs) == 0 {
		return
	}
	last := lastAssistantResponse(msgs)
	if strings.TrimSpace(last) != "" {
		fmt.Fprintln(c.Out, st.BoldYellow("Last assistant response"))
		fmt.Fprintln(c.Out, trimForDisplay(last, 1500))
	}
}

func resolveSessionID(c *Ctx, raw string) (string, error) {
	target := strings.TrimSpace(raw)
	if target == "" {
		return "", fmt.Errorf("session id is empty")
	}
	infos, err := session.ListSessions(c.Cfg)
	if err != nil {
		return "", err
	}
	if len(infos) == 0 {
		return "", fmt.Errorf("no saved sessions found; run /sessions")
	}
	for _, info := range infos {
		if info.ID == target {
			return info.ID, nil
		}
	}
	matches := make([]string, 0, 4)
	for _, info := range infos {
		if strings.HasPrefix(info.ID, target) {
			matches = append(matches, info.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("session %q not found; run /sessions to list valid ids", target)
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("session prefix %q is ambiguous (%s); provide more characters", target, strings.Join(matches, ", "))
	}
}

func lastAssistantResponse(msgs []session.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != "assistant" {
			continue
		}
		content := strings.TrimSpace(m.Content)
		if content == "" || strings.HasPrefix(content, "[runtime]") {
			continue
		}
		return content
	}
	return ""
}

func trimForDisplay(s string, maxChars int) string {
	text := strings.TrimSpace(collapseWhitespace(s))
	if text == "" || maxChars <= 0 {
		return ""
	}
	if len(text) <= maxChars {
		return text
	}
	return text[:maxChars] + "..."
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
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
