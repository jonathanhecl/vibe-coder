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

func runInitialPrompt(rootCtx context.Context, cfg *config.Config, ag *agent.Agent, sess *session.Session, ui tui.UI, resumed bool) (bool, error) {
	// Keep one-shot output aligned with interactive startup context so users
	// can always see which model/session/host served the answer.
	fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), resumed, tui.NewStyle(os.Stdout)))
	if err := runPrompt(rootCtx, ag, ui, cfg.Prompt); err != nil {
		return false, err
	}
	if cfg == nil || !cfg.Temporal {
		if err := sess.Save(); err != nil {
			return false, fmt.Errorf("failed to save session: %w", err)
		}
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
			if cfg == nil || !cfg.Temporal {
				if sess != nil && sess.MessageCount() > 0 {
					if err := sess.Save(); err != nil {
						fmt.Fprintf(os.Stderr, "error: failed to save session: %v\n", err)
					}
				}
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
	// Log even when the mission finished inside the first turn (small batches
	// can complete within the per-turn iteration budget).
	if ag.MissionSnapshot().Status != "" {
		ag.LogMissionTurn(err)
		fmt.Fprintf(os.Stdout, "[mission] run log: %s\n", ag.RunLogPath())
	}
	emptyStreak := 0
	idleStreak := 0
	for ag.MissionActive() {
		if ctxErr := rootCtx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil && !agent.IsIterationCapErr(err) && !agent.IsEmptyAssistantResponseErr(err) {
			if agent.IsNoProgressErr(err) {
				// The model is repeating the same tool call with identical output.
				// Pause instead of spinning forever (a mission has no turn limit).
				ag.BlockActiveMission("The agent repeated the same tool call with identical output; mission paused to avoid an infinite loop.")
				fmt.Fprintln(os.Stderr, "Mission paused: repeated tool call with no progress. The agent needs your input.")
				saveMissionProgress(ag)
				ag.LogMissionTurn(err)
				break
			}
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
			ag.LogMissionTurn(err)
			break
		}
		// Stall guard: a turn that executed no tool at all did no work. This is
		// progress-based, not a turn budget: a mission doing real work keeps
		// running indefinitely.
		if ag.ToolCallsThisTurn() == 0 {
			idleStreak++
		} else {
			idleStreak = 0
		}
		if ag.PendingTodoCount() == 0 || idleStreak > 0 {
			if completed, checkErr := ag.CheckMissionCompletion(rootCtx); checkErr == nil && completed {
				ag.CompleteMission("Completed (verified by JEV Style)")
				fmt.Fprintln(os.Stdout, "[mission] JEV Style verified the goal is accomplished; mission completed.")
				saveMissionProgress(ag)
				ag.LogMissionTurn(err)
				break
			}
		}
		if idleStreak >= 3 {
			ag.BlockActiveMission("Several consecutive turns ran no tools; mission paused so the agent does not spin.")
			fmt.Fprintln(os.Stderr, "Mission paused: no tool activity for several turns. The agent needs your input.")
			saveMissionProgress(ag)
			ag.LogMissionTurn(err)
			break
		}
		m := ag.MissionSnapshot()
		fmt.Fprintf(os.Stdout, "[mission] continuing autonomously (turn %d)\n", m.Turns+1)
		err = runAgentWithEmptyRetry(rootCtx, ag, ui, ag.MissionContinuationPrompt())
		// Flush each turn so a crash or restart resumes the mission where it left off.
		saveMissionProgress(ag)
		ag.LogMissionTurn(err)
	}
	if err != nil && !agent.IsIterationCapErr(err) && !agent.IsNoProgressErr(err) {
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
		// Wrap each ag.Run in a child context that the signal handler can
		// cancel without killing the root context, so a Ctrl+C interrupts
		// only the current operation and the REPL stays alive.
		opCtx, opCancel := context.WithCancel(rootCtx)
		globalInterrupter.arm(opCancel)
		err := ag.Run(opCtx, currentInput)
		opCancel()
		globalInterrupter.disarm()
		if err == nil {
			return nil
		}
		if !agent.IsEmptyAssistantResponseErr(err) {
			return err
		}
		if !stdioIsTTY() {
			return err
		}
		// An active mission runs unattended: retry automatically instead of
		// blocking on a TTY question. The mission loop ends the mission if the
		// empty responses persist.
		unattended := ag.MissionActive()
		retryCount++
		repeatedState = retryCount > 1
		if retryCount >= maxExternalEmptyRetries {
			return fmt.Errorf("empty assistant response persisted after %d retries; pending TODO state may be stuck", retryCount)
		}
		if unattended {
			currentInput = ag.BuildEmptyResponseRetryInput(input, repeatedState)
			continue
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
