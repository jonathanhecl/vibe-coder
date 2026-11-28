package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/lsp"
	"github.com/jonathanhecl/vibe-coder/internal/mcp"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/slash"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

func runInitialPrompt(rootCtx context.Context, cfg *config.Config, ag *agent.Agent, sess *session.Session, ui tui.UI, resumed bool, mcpMgr *mcp.Manager, offlineModels map[config.Role]bool) (bool, error) {
	// Keep one-shot output aligned with interactive startup context so users
	// can always see which model/session/host served the answer.
	fmt.Fprint(os.Stdout, startupBanner(cfg, sess.ID(), resumed, tui.NewStyle(os.Stdout), offlineModels))
	printMCPBlock(os.Stdout, tui.NewStyle(os.Stdout), mcpMgr)
	// One-shot continuations and goal runs never approve stale work: they
	// surface ErrStaleChecklist so the user opens an interactive session.
	if cfg != nil && (agent.IsContinuationPrompt(cfg.Prompt) || cfg.Goal) && ag.StaleChecklist(time.Now()) {
		return false, agent.ErrStaleChecklist
	}
	if cfg != nil && cfg.Goal && !ag.MissionActive() && strings.TrimSpace(cfg.Prompt) != "" {
		if ag.MissionSnapshot().Status == tools.MissionStatusBlocked && agent.IsContinuationPrompt(cfg.Prompt) {
			ag.ResumeMission()
		} else {
			ag.StartMission(cfg.Prompt)
		}
	}
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

func runInteractiveREPL(rootCtx context.Context, cfg *config.Config, client ollama.Client, ag *agent.Agent, sess *session.Session, perm *permissions.Manager, reg *tools.Registry, uiBox *uiHolder, ctxStore *contextfiles.Store, mcpMgr *mcp.Manager, lspMgr *lsp.Manager) {
	// Model listing (/models) should come from the default provider, not from
	// the main model's chat client, so capabilities match the provider's API.
	// The main client stays available for live capability probes.
	var defaultLister ollama.Client
	if provider, ok := cfg.Provider(config.DefaultProviderName); ok {
		defaultLister = slash.ProviderClient(provider)
	}
	slashCtx := &slash.Ctx{
		Cfg:           cfg,
		Session:       sess,
		Perm:          perm,
		Agent:         ag,
		Client:        client,
		Models:        client,
		DefaultLister: defaultLister,
		Out:           os.Stdout,
		Contexts:      ctxStore,
		Prompter:      uiBox.Get(),
		MCP:           mcpMgr,
		LSP:           lspMgr,
	}
	slashCtx.OnUIChange = makeUISwitcher(cfg, ag, sess, reg, uiBox, slashCtx)

	resume := func(ui tui.UI) {
		if !ag.CanResume() {
			return
		}
		st := tui.NewStyle(os.Stdout)
		fmt.Fprintln(os.Stdout, st.Yellow("↩ Continuing from checkpoint..."))
		ui.SetPlanMode(ag.InPlanMode())
		finishTurn(rootCtx, cfg, sess, ag, ui, runContinuationPrompt(rootCtx, ag, ui))
		_ = sess.Save()
	}
	review := func(ui tui.UI) {
		asker, ok := ui.(tui.OptionAsker)
		if !ok || !stdioIsTTY() {
			fmt.Fprintln(os.Stderr, agent.ErrStaleChecklist)
			return
		}
		approved, err := reviewStaleChecklist(ag, sess, asker, time.Now)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return
		}
		if approved {
			resume(ui)
		}
	}
	var lastSessionID string

	for {
		ui := uiBox.Get()
		// A session switch (/resume, /session) can land a stale checklist:
		// ask once per entry so Later does not reprompt every turn.
		if id := sess.ID(); id != lastSessionID {
			lastSessionID = id
			if now := time.Now(); stdioIsTTY() && ag.StaleChecklist(now) {
				review(ui)
			}
		}
		ui.SetPlanMode(ag.InPlanMode())
		syncUIStatus(ui, cfg, ag, sess)
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
		if line == "" || agent.IsContinuationPrompt(line) {
			if now := time.Now(); ag.StaleChecklist(now) {
				review(ui)
				continue
			}
			if line == "" {
				resume(ui)
				continue
			}
		}
		if _, startsGoal := slash.GoalTaskFromSlash(line); startsGoal && ag.StaleChecklist(time.Now()) {
			fmt.Fprintln(os.Stderr, "Review the pending checklist before starting a new goal. Press Enter to continue or discard it.")
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
			finishTurn(rootCtx, slashCtx.Cfg, slashCtx.Session, ag, ui, runAgentWithEmptyRetry(rootCtx, ag, ui, task))
			_ = slashCtx.Session.Save()
			ui.SetPlanMode(ag.InPlanMode())
			return false
		}
		if task, ok := reviewTaskFromSlash(line); ok {
			finishTurn(rootCtx, slashCtx.Cfg, slashCtx.Session, ag, ui, runAgentWithEmptyRetry(rootCtx, ag, ui, task))
			_ = slashCtx.Session.Save()
			ag.ExitReviewMode()
			ui.SetPlanMode(ag.InPlanMode())
			return false
		}
		if task, ok := slash.PasteTaskFromSlash(slashCtx, line); ok {
			finishTurn(rootCtx, slashCtx.Cfg, slashCtx.Session, ag, ui, runAgentWithEmptyRetry(rootCtx, ag, ui, task))
			_ = slashCtx.Session.Save()
			ui.SetPlanMode(ag.InPlanMode())
			return false
		}
		if task, ok := slash.GoalTaskFromSlash(line); ok {
			if ag.MissionActive() && ag.MissionSnapshot().Goal == task {
				prompt := slash.FormatGoalPrompt(task)
				finishTurn(rootCtx, slashCtx.Cfg, slashCtx.Session, ag, ui, runPrompt(rootCtx, ag, ui, prompt))
				_ = slashCtx.Session.Save()
				return false
			}
		}
		return false
	}

	ui.SetPlanMode(ag.InPlanMode())
	finishTurn(rootCtx, slashCtx.Cfg, slashCtx.Session, ag, ui, runPrompt(rootCtx, ag, ui, line))
	_ = slashCtx.Session.Save()
	return false
}

// printTurnError names an interruption and an error plainly, without the
// pending-work notice. /autocontinue prints that notice only once, when the
// loop really hands the prompt back.
func printTurnError(err error) {
	switch {
	case err == nil:
	case agent.IsTurnCancelledErr(err):
		st := tui.NewStyle(os.Stdout)
		fmt.Fprintln(os.Stderr, st.Yellow("Turn cancelled — the session, the checklist and every tool result are intact."))
	default:
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
	}
}

// reportPendingWork prints one line about the checklist and mission state the
// finished turn left behind, so the user always knows whether something is
// still open before typing the next prompt.
func reportPendingWork(ag *agent.Agent, ui tui.UI) {
	if ag == nil {
		return
	}
	// A queued prompt runs by itself as soon as this function returns; telling
	// the user to press Enter would be wrong.
	if q, ok := ui.(interface{ QueuedPrompts() int }); ok && q.QueuedPrompts() > 0 {
		return
	}
	summary := ag.PendingWorkSummary()
	if summary == "" {
		return
	}
	st := tui.NewStyle(os.Stdout)
	fmt.Fprintf(os.Stdout, "%s %s\n", st.Yellow("⏳"), summary)
}

// autoContinueTurnCap bounds how many turns one prompt may continue on its
// own. A model that never finishes has to hand the prompt back instead of
// spending the night on the same task. A variable so a test can lower it.
var autoContinueTurnCap = 20

const (
	// autoContinueIdleTurns is how many consecutive automatic turns may run no
	// tool at all before the loop returns to the prompt: asking again only
	// repeats the same request to the same model.
	autoContinueIdleTurns = 2
	// autoContinueInput is what a continuation sends when there is no durable
	// checkpoint to resume (for example the step cap hit mid-inspection). It is
	// the same shorthand a user types, so goal resolution and checklist
	// handling treat it as a continuation.
	autoContinueInput = "continue"
	// autoContinueStallStops is how many consecutive automatic turns may end on
	// an anti-loop guard before the loop hands the prompt back. One more turn
	// often unsticks the model; the same stop twice is it asking for help.
	autoContinueStallStops = 2
)

// isStallStop reports a turn its own anti-loop guard stopped: the model lurched
// (repeated answer, repeated call, inspection-only) instead of finishing. It is
// not a hard failure, so an automatic continuation gets another turn before the
// guard wins.
func isStallStop(err error) bool {
	return agent.IsNoProgressErr(err) ||
		agent.IsRepeatedReplyErr(err) ||
		agent.IsInspectionLoopErr(err) ||
		agent.IsThinkingLoopStopErr(err)
}

// autoContinueStopReason names, for the user, why a stop is not continuable. It
// returns "" for the ordinary end of a task, where there is nothing to explain.
func autoContinueStopReason(ag *agent.Agent, err error) string {
	if ag == nil {
		return "the agent is not available"
	}
	switch {
	case ag.InPlanMode() || ag.InReviewMode():
		return "plan and review modes are interactive"
	case ag.MissionSnapshot().Status == tools.MissionStatusBlocked:
		return "the goal is paused waiting for your input"
	case ag.StaleChecklist(time.Now()):
		return "the checklist needs review before it can continue"
	case agent.IsTurnCancelledErr(err):
		return "the turn was cancelled"
	case err == nil && !ag.CanResume():
		return ""
	case err == nil:
		return "there is nothing resumable"
	default:
		return "the turn stopped on an error"
	}
}

// autoContinueEligible reports whether a finished turn stopped in a way a
// continuation can pick up. Everything else - a finished task, a question, a
// cancellation, a paused goal, plan/review mode, a hard error - goes back to
// the prompt, because only a human can move it forward.
func autoContinueEligible(ag *agent.Agent, err error) bool {
	if ag == nil {
		return false
	}
	// A plan or a review is a conversation with the user, not unattended work.
	if ag.InPlanMode() || ag.InReviewMode() {
		return false
	}
	// A paused goal explicitly asked for the user; continuing would undo the
	// pause the agent just declared.
	if m := ag.MissionSnapshot(); m.Status == tools.MissionStatusBlocked {
		return false
	}
	// A leftover checklist from an earlier session needs review first, and the
	// interactive path asks about it. An unattended loop must not bypass that.
	if ag.StaleChecklist(time.Now()) {
		return false
	}
	switch {
	case err == nil:
		return ag.CanResume()
	case agent.IsIterationCapErr(err):
		// The step budget ran out, not the work: a continuation resumes the
		// checkpoint, the pending checklist, or a turn that did run tools.
		return ag.CanResume() || ag.PendingTodoCount() > 0 || ag.ToolCallsThisTurn() > 0
	case isStallStop(err):
		// An anti-loop guard fired: the turn lurched, it did not finish, so a
		// continuation gets another chance while there is work to continue.
		return ag.CanResume() || ag.PendingTodoCount() > 0
	default:
		return false
	}
}

// finishTurn reports one turn and then honours /autocontinue: while the mode is
// on and the stop is one a continuation can pick up, it starts the next turn by
// itself. The pending-work notice is printed once, when the loop really hands
// control back, so "Press Enter to continue" never appears right before the
// agent continues on its own.
func finishTurn(rootCtx context.Context, cfg *config.Config, sess *session.Session, ag *agent.Agent, ui tui.UI, err error) error {
	return finishTurnWith(rootCtx, cfg, sess, ag, ui, err, func() error {
		if ag.CanResume() {
			return runContinuationPrompt(rootCtx, ag, ui)
		}
		return runPrompt(rootCtx, ag, ui, autoContinueInput)
	})
}

// finishTurnWith is finishTurn with the turn itself injected, so a test can
// exercise the loop limits without a model.
func finishTurnWith(rootCtx context.Context, cfg *config.Config, sess *session.Session, ag *agent.Agent, ui tui.UI, err error, run func() error) error {
	printTurnError(err)
	if agent.IsTurnCancelledErr(err) {
		// Ctrl+C stops the work; it must not trigger the queue on the way out.
		if holder, ok := ui.(interface{ HoldQueuedPrompts() }); ok {
			holder.HoldQueuedPrompts()
		}
	}
	st := tui.NewStyle(os.Stdout)
	eligible := autoContinueEligible(ag, err)
	if cfg == nil || !cfg.AutoContinue || !eligible {
		if cfg != nil && cfg.AutoContinue && !eligible {
			// A stop /autocontinue deliberately returns on: say why in the chat
			// and in the log, so "it still asked for Enter" can be explained.
			if reason := autoContinueStopReason(ag, err); reason != "" {
				fmt.Fprintf(os.Stderr, "%s Auto-continue paused: %s.\n", st.Yellow("⏳"), reason)
			}
			logger.Infof("auto-continue stopped: err=%v canResume=%t pending=%d plan=%t review=%t stale=%t stopReason=%q",
				err, ag.CanResume(), ag.PendingTodoCount(), ag.InPlanMode(), ag.InReviewMode(),
				ag.StaleChecklist(time.Now()), ag.LastStopReason())
		}
		reportPendingWork(ag, ui)
		return err
	}
	turns, idle, stalls := 0, 0, 0
	for cfg.AutoContinue && autoContinueEligible(ag, err) {
		if rootCtx.Err() != nil {
			break
		}
		if turns >= autoContinueTurnCap {
			// The mode stays on: the cap bounds this prompt, not the session, and
			// silently turning it off is what made it look like it had no effect.
			fmt.Fprintf(os.Stderr, "%s Auto-continue paused after %d turns to avoid an endless loop. Work state is saved; press Enter to continue (the mode stays on).\n", st.Yellow("⏳"), autoContinueTurnCap)
			break
		}
		turns++
		fmt.Fprintf(os.Stdout, "%s [autocontinue] continuing (turn %d/%d)\n", st.Dim("↻"), turns, autoContinueTurnCap)
		before := ag.PendingTodoCount()
		err = run()
		if sess != nil {
			if saveErr := sess.Save(); saveErr != nil {
				fmt.Fprintf(os.Stderr, "warning: failed to save session: %v\n", saveErr)
			}
		}
		printTurnError(err)
		// A turn that ran no tool and did not move the checklist is not
		// progress; two in a row are the model restating itself.
		if err == nil && ag.ToolCallsThisTurn() == 0 && ag.PendingTodoCount() >= before {
			idle++
		} else {
			idle = 0
		}
		if isStallStop(err) {
			stalls++
		} else {
			stalls = 0
		}
		if stalls >= autoContinueStallStops {
			fmt.Fprintf(os.Stderr, "%s Auto-continue paused: the model hit the same anti-loop guard %d turns in a row. Press Enter to retry, or type a correction.\n", st.Yellow("⏳"), stalls)
			break
		}
		if idle >= autoContinueIdleTurns {
			fmt.Fprintf(os.Stderr, "%s Auto-continue paused: %d turns in a row ran no tool and left the checklist unchanged. Press Enter to retry, or type a correction.\n", st.Yellow("⏳"), idle)
			break
		}
	}
	reportPendingWork(ag, ui)
	return err
}

// runPrompt runs one user prompt and then honours any mission the agent
// declared during it. While a mission is active the runtime keeps starting
// turns until the agent calls MissionComplete or MissionBlocked; there is no
// turn limit. The agent owns the mission lifecycle.
func runPrompt(rootCtx context.Context, ag *agent.Agent, ui tui.UI, input string) error {
	return runPromptWithStarter(rootCtx, ag, ui, func(ctx context.Context) error {
		return runAgentWithEmptyRetry(ctx, ag, ui, input)
	})
}

// runContinuationPrompt starts from the durable checkpoint and keeps the same
// mission-aware turn handling. The first turn adds no user message.
func runContinuationPrompt(rootCtx context.Context, ag *agent.Agent, ui tui.UI) error {
	return runPromptWithStarter(rootCtx, ag, ui, func(ctx context.Context) error {
		return runContinuationWithRetry(ctx, ag, ui)
	})
}

func runPromptWithStarter(rootCtx context.Context, ag *agent.Agent, ui tui.UI, start func(context.Context) error) error {
	// One evidence window per user invocation: completed checklist items from
	// before this run are the baseline, and the stall guard only credits new
	// tool evidence gathered while this run is alive.
	rootCtx, progress := ag.TrackMissionProgress(rootCtx)
	err := start(rootCtx)
	saveMissionProgress(ag)
	// Log even when the mission finished inside the first turn (small batches
	// can complete within the per-turn iteration budget).
	if ag.MissionSnapshot().Status != "" {
		ag.LogMissionTurn(err)
		if logPath := ag.RunLogPath(); logPath != "" {
			fmt.Fprintf(os.Stdout, "[mission] run log: %s\n", logPath)
		}
	}
	emptyStreak := 0
	stalledTurns := 0
	// A pause already told the user why the mission stopped; reporting the same
	// error again adds noise, not information.
	pauseReported := false
	for ag.MissionActive() {
		if ctxErr := rootCtx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err == nil && ag.PendingTodoCount() == 0 {
			ag.CompleteMission("Completed")
			fmt.Fprintln(os.Stdout, "[goal] Autonomous goal completed.")
			saveMissionProgress(ag)
			ag.LogMissionTurn(nil)
			break
		}
		if err != nil && !agent.IsIterationCapErr(err) && !agent.IsEmptyAssistantResponseErr(err) {
			if agent.IsNoProgressErr(err) {
				// Pause instead of spinning forever (a mission has no turn limit),
				// and preserve which kind of stall the user needs to correct.
				reason := "The agent repeated the same tool call with identical output; mission paused to avoid an infinite loop."
				message := "Mission paused: repeated tool call with no progress. The agent needs your input."
				if agent.IsInspectionLoopErr(err) {
					reason = "The agent kept inspecting unchanged state without taking a concrete action; mission paused to avoid an inspection-only loop."
					message = "Mission paused: repeated inspection without concrete action. The agent needs your input."
				}
				ag.BlockActiveMission(reason)
				fmt.Fprintln(os.Stderr, message)
				saveMissionProgress(ag)
				ag.LogMissionTurn(err)
				pauseReported = true
				break
			}
			if agent.IsRepeatedReplyErr(err) {
				// The model kept restating the same answer while the checklist
				// stood still: pausing is what stops a mission from spending
				// every remaining turn on the same paragraph.
				ag.BlockActiveMission("The agent kept repeating the same answer while checklist items stayed pending; mission paused to avoid an infinite loop.")
				fmt.Fprintln(os.Stderr, "Mission paused: repeated answer with no progress. The agent needs your input.")
				saveMissionProgress(ag)
				ag.LogMissionTurn(err)
				pauseReported = true
				break
			}
			if agent.IsThinkingLoopStopErr(err) {
				// The model's reasoning kept looping without an answer; pause
				// the mission so the user can resume, rephrase, or dampen
				// thinking instead of burning turns on the same loop.
				ag.BlockActiveMission("The model's reasoning looped repeatedly without reaching an answer; mission paused.")
				fmt.Fprintln(os.Stderr, "Mission paused: the model's reasoning kept looping. The agent needs your input.")
				saveMissionProgress(ag)
				ag.LogMissionTurn(err)
				pauseReported = true
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
		if emptyStreak >= 2 {
			ag.BlockActiveMission("The model returned repeated empty responses; mission paused for user input.")
			fmt.Fprintln(os.Stderr, "Mission paused: repeated empty responses. The agent needs your input.")
			saveMissionProgress(ag)
			ag.LogMissionTurn(err)
			pauseReported = true
			break
		}
		// Stall guard: a turn is productive only when it adds new evidence or
		// completes checklist items. Repeating identical calls with identical
		// outputs across turns is not progress, even if each turn ran tools.
		if progress.Advanced(ag) {
			stalledTurns = 0
		} else {
			stalledTurns++
		}
		if stalledTurns >= 3 {
			ag.BlockActiveMission("Three consecutive turns produced no new evidence or checklist progress.")
			fmt.Fprintln(os.Stderr, "Mission paused: no new progress for three turns. Press Enter to resume, or type a correction.")
			ag.PersistWorkState()
			saveMissionProgress(ag)
			ag.LogMissionTurn(err)
			pauseReported = true
			break
		}
		m := ag.MissionSnapshot()
		fmt.Fprintf(os.Stdout, "[mission] continuing autonomously (turn %d)\n", m.Turns+1)
		next := ag.MissionContinuationPrompt()
		if stalledTurns == 2 {
			next += " The last two turns produced no new progress. Use the evidence already gathered. Take a different concrete action, finish if the goal is satisfied, or identify the missing fact or decision; do not repeat the same checks on unchanged inputs."
		}
		err = runAgentWithEmptyRetry(rootCtx, ag, ui, next)
		// Flush each turn so a crash or restart resumes the mission where it left off.
		saveMissionProgress(ag)
		ag.LogMissionTurn(err)
	}
	// A mission that exhausted its per-turn step budget simply continues in the
	// next turn; every other stop must reach the caller. Swallowing the step cap
	// and the repeated-call stop here is what dropped the user back to the
	// prompt with no answer and no reason.
	if err != nil && !pauseReported && !(agent.IsIterationCapErr(err) && ag.MissionSnapshot().Status != "") {
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
	return runAgentTurnWithRetry(rootCtx, ag, ui, input, false)
}

func runContinuationWithRetry(rootCtx context.Context, ag *agent.Agent, ui tui.UI) error {
	return runAgentTurnWithRetry(rootCtx, ag, ui, "", true)
}

func runAgentTurnWithRetry(rootCtx context.Context, ag *agent.Agent, ui tui.UI, input string, continuation bool) error {
	retryCount := 0
	repeatedState := false
	currentInput := strings.TrimSpace(input)
	for {
		// Wrap each run in a child context that the signal handler can cancel
		// without killing the root context, so a Ctrl+C interrupts only the
		// current operation and the REPL stays alive.
		opCtx, opCancel := context.WithCancel(rootCtx)
		globalInterrupter.arm(opCancel)
		var err error
		if continuation {
			err = ag.RunContinuation(opCtx)
		} else {
			err = ag.Run(opCtx, currentInput)
		}
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
			if !continuation {
				currentInput = ag.BuildEmptyResponseRetryInput(input, repeatedState)
			}
			continue
		}
		ans, askErr := ui.GetInput("Model returned an empty response. Retry this step? [Y/n]: ")
		if askErr != nil {
			return err
		}
		a := strings.ToLower(strings.TrimSpace(ans))
		if a == "" || a == "y" || a == "yes" {
			if !continuation {
				currentInput = ag.BuildEmptyResponseRetryInput(input, repeatedState)
			}
			continue
		}
		// The user declined the retry: say so instead of returning to the prompt
		// in silence, which reads like a crash.
		fmt.Fprintln(os.Stdout, "No retry requested; this turn ended without an answer.")
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
