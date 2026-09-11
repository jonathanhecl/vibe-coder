package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/slash"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runInitialPrompt(rootCtx context.Context, cfg *config.Config, ag *agent.Agent, sess *session.Session, ui tui.UI) (bool, error) {
	// Keep one-shot output aligned with interactive startup context so users
	// can always see which model/session/host served the answer.
	fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), tui.NewStyle(os.Stdout)))
	if err := runPrompt(rootCtx, ag, ui, cfg.Prompt); err != nil {
		return false, err
	}
	if err := sess.Save(); err != nil {
		return false, fmt.Errorf("failed to save session: %w", err)
	}
	return shouldContinueInteractiveAfterPrompt(
		cfg,
		stdinIsTTY(),
		stdoutIsTTY(),
	), nil
}

func runInteractiveREPL(rootCtx context.Context, cfg *config.Config, client ollama.Client, ag *agent.Agent, sess *session.Session, perm *permissions.Manager, ui tui.UI, ctxStore *contextfiles.Store) {
	slashCtx := &slash.Ctx{
		Cfg:      cfg,
		Session:  sess,
		Perm:     perm,
		Agent:    ag,
		Client:   client,
		Out:      os.Stdout,
		Contexts: ctxStore,
		Prompter: ui,
	}

	for {
		ui.SetPlanMode(ag.InPlanMode())
		line, err := ui.GetInput("> ")
		if err != nil {
			if err := sess.Save(); err != nil {
				fmt.Fprintf(os.Stderr, "error: failed to save session: %v\n", err)
			}
			printByeOnInterrupt()
			return
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if shouldExit := handleInputLine(rootCtx, slashCtx, ag, ui, line); shouldExit {
			return
		}
	}
}

func handleInputLine(rootCtx context.Context, slashCtx *slash.Ctx, ag *agent.Agent, ui tui.UI, line string) bool {
	handled, shouldExit, err := slash.Dispatch(slashCtx, line)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return false
	}
	if shouldExit {
		fmt.Fprintln(os.Stdout, "Bye.")
		return true
	}
	if handled {
		ui.SetPlanMode(ag.InPlanMode())
		if task, ok := planTaskFromSlash(line); ok {
			if err := runAgentWithEmptyRetry(rootCtx, ag, ui, task); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			_ = slashCtx.Session.Save()
			ui.SetPlanMode(ag.InPlanMode())
			return false
		}
		if task, ok := reviewTaskFromSlash(line); ok {
			if err := runAgentWithEmptyRetry(rootCtx, ag, ui, task); err != nil {
				fmt.Fprintf(os.Stderr, "error: %v\n", err)
			}
			_ = slashCtx.Session.Save()
			ag.ExitReviewMode()
			ui.SetPlanMode(ag.InPlanMode())
			return false
		}
		return false
	}

	ui.SetPlanMode(ag.InPlanMode())
	if err := runPrompt(rootCtx, ag, ui, line); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
	_ = slashCtx.Session.Save()
	return false
}

// runPrompt runs one user prompt and then honours any mission the agent
// declared during it. While a mission is active the runtime keeps starting
// turns until the agent calls MissionComplete or MissionBlocked; there is no
// turn limit. The agent owns the mission lifecycle.
func runPrompt(rootCtx context.Context, ag *agent.Agent, ui tui.UI, input string) error {
	err := runAgentWithEmptyRetry(rootCtx, ag, ui, input)
	saveMissionProgress(ag)
	emptyStreak := 0
	for ag.MissionActive() {
		if ctxErr := rootCtx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil && !agent.IsIterationCapErr(err) && !agent.IsEmptyAssistantResponseErr(err) {
			// A hard failure (network, permission, cancellation) stops the run.
			return err
		}
		if agent.IsEmptyAssistantResponseErr(err) {
			emptyStreak++
		} else {
			emptyStreak = 0
		}
		if emptyStreak >= 3 {
			ag.BlockActiveMission("The model returned repeated empty responses; mission paused for user input.")
			fmt.Fprintln(os.Stderr, "Mission paused: repeated empty responses. The agent needs your input.")
			saveMissionProgress(ag)
			break
		}
		m := ag.MissionSnapshot()
		fmt.Fprintf(os.Stdout, "[mission] continuing autonomously (turn %d)\n", m.Turns+1)
		err = runAgentWithEmptyRetry(rootCtx, ag, ui, ag.MissionContinuationPrompt())
		// Flush each turn so a crash or restart resumes the mission where it left off.
		saveMissionProgress(ag)
	}
	if err != nil && !agent.IsIterationCapErr(err) {
		return err
	}
	return nil
}

// saveMissionProgress flushes the session (mission, checklist, tasks) to disk.
// Failures warn but never break the autonomous run.
func saveMissionProgress(ag *agent.Agent) {
	if err := ag.SaveSession(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save mission progress: %v\n", err)
	}
}

func runAgentWithEmptyRetry(rootCtx context.Context, ag *agent.Agent, ui tui.UI, input string) error {
	retryCount := 0
	repeatedState := false
	currentInput := strings.TrimSpace(input)
	for {
		err := ag.Run(rootCtx, currentInput)
		if err == nil {
			return nil
		}
		if !agent.IsEmptyAssistantResponseErr(err) {
			return err
		}
		if !stdioIsTTY() {
			return err
		}
		retryCount++
		repeatedState = retryCount > 1
		if retryCount >= maxExternalEmptyRetries {
			return fmt.Errorf("empty assistant response persisted after %d retries; pending TODO state may be stuck", retryCount)
		}
		ans, askErr := ui.GetInput("Model returned an empty response. Retry this step? [Y/n]: ")
		if askErr != nil {
			return err
		}
		a := strings.ToLower(strings.TrimSpace(ans))
		if a == "" || a == "y" || a == "yes" {
			currentInput = ag.BuildEmptyResponseRetryInput(input, repeatedState)
			continue
		}
		return nil
	}
}

// planTaskFromSlash extracts an immediate planning goal from "/plan <goal>".
// Control forms like "/plan off|exit|cancel" do not return a task.
func planTaskFromSlash(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/plan") {
		return "", false
	}
	fields := strings.Fields(trimmed)
	if len(fields) < 2 {
		return "", false
	}
	switch strings.ToLower(strings.TrimSpace(fields[1])) {
	case "off", "exit", "cancel":
		return "", false
	}
	task := strings.TrimSpace(strings.TrimPrefix(trimmed, fields[0]))
	if task == "" {
		return "", false
	}
	return task, true
}

// reviewTaskFromSlash extracts the prompt from "/review <prompt>".
func reviewTaskFromSlash(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, "/review") {
		return "", false
	}
	task := strings.TrimSpace(strings.TrimPrefix(trimmed, "/review"))
	if task == "" {
		return "", false
	}
	return task, true
}
