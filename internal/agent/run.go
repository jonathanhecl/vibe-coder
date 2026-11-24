package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/terminal"
)

type toolExecutionMode struct {
	// The inferred-tool and XML-fallback paths historically surfaced denial
	// differently. Keep that UI detail explicit while sharing execution logic.
	showPermissionDeniedResult bool
	// endAssistantOnCancel closes the assistant block when the user cancels a
	// run that never streamed an assistant message of its own.
	endAssistantOnCancel bool
	// structured marks native tool-call turns: their results persist as
	// role "tool" messages instead of the XML-fallback user envelope.
	structured bool
	// skipSessionRecord prevents delegated/child executions from polluting
	// the parent session's transcript.
	skipSessionRecord bool
	// auxiliaryNotes defers post-edit notes, diagnostics, and auto-tests
	// until all native tool results in a batch are recorded.
	auxiliaryNotes *[]func()
}

// toolCallsToWire converts internal tool calls back to Ollama's wire shape
// for history replay.
func toolCallsToWire(calls []ToolCall) []ollama.MessageToolCall {
	out := make([]ollama.MessageToolCall, 0, len(calls))
	for _, c := range calls {
		out = append(out, ollama.MessageToolCall{Function: ollama.MessageToolCallFunction{
			Name:      c.Name,
			Arguments: c.Params,
		}})
	}
	return out
}

// Run executes one normal user turn. Typed corrections and continuation
// shorthands keep their existing transcript behavior.
func (a *Agent) Run(rootCtx context.Context, userInput string) error {
	return a.run(rootCtx, userInput, false)
}

// run executes one turn. A continuation turn starts from the durable checkpoint
// and deliberately adds no user message, so bare Enter cannot steer the model
// with empty or boilerplate input.
func (a *Agent) run(rootCtx context.Context, userInput string, continuation bool) (err error) {
	if continuation && !a.CanResume() {
		return ErrNothingToResume
	}
	ctx, cancel := context.WithCancel(rootCtx)
	defer cancel()
	defer terminal.DefaultManager().TerminateAll()
	if err := a.ui.StartESCMonitor(cancel); err != nil {
		return err
	}
	defer a.ui.StopESCMonitor()
	// A new turn measures its own duration: restart the UI turn timer here so
	// the "responded in" footer covers this request instead of the session.
	a.ui.StartTurn()
	// Persist the checklist/task state and the continuation cursor at the end
	// of every turn so a crash or restart never loses where the work stopped.
	defer func() {
		a.finishRunCheckpoint(err)
		a.PersistWorkState()
	}()
	// Notes belong to one session id: drop them when the session changed
	// since they were captured or restored.
	a.ensureNotesSession()

	a.resetTurnToolCalls()
	// The repeated-call budget is per turn: a streak left by an earlier request
	// must not abort this one on its first identical call.
	a.noProgress.reset()
	// The inspection-only budget is also scoped to this turn.
	a.inspection.reset()
	// The repeated-answer budget is per turn for the same reason.
	a.replyRepeat.reset()
	var resumed *resumeCheckpoint
	if continuation {
		cp, ok := a.beginContinuation()
		if !ok {
			return ErrNothingToResume
		}
		resumed = &cp
	}
	// Keep permission mode in sync with the mission at the start of every turn
	// so unattended approval can never leak into a non-mission run.
	if a.perm != nil {
		a.perm.SetUnattended(a.MissionActive())
	}
	if resumed != nil {
		a.addContinuationContext(ctx, *resumed)
	} else {
		a.addRunContext(ctx, userInput)
	}

	// ParallelAgents is explicit-only: the model calls it when a task is
	// genuinely decomposable. Previous auto-detection split any message
	// containing " and " or numbered lines, hijacking normal requests.
	toolName, toolParams, wantsTool := inferSingleToolCall(userInput)
	thinkingOnlyRetries := 0
	emptyChatErrRetries := 0
	noProgressRetries := 0
	unknownToolRounds := 0
	thinkingLoopStops := 0
	for i := 0; i < MaxIterations; i++ {
		// A Ctrl+C between calls ends the turn here. Every further iteration
		// would only replay an already cancelled request, which is what kept
		// the loop spinning to the step cap while the user pressed Ctrl+C.
		if ctx.Err() != nil {
			return turnCancelledErr(ctx.Err())
		}
		if wantsTool {
			tool := a.reg.Get(toolName)
			if tool == nil {
				return fmt.Errorf("tool not found: %s", toolName)
			}
			if _, outcome, err := a.executeTool(ctx, tool, toolName, toolParams, toolExecutionMode{
				endAssistantOnCancel: true,
			}); err != nil {
				if a.recoverNoProgress(ctx, err, &noProgressRetries) {
					wantsTool = false
					continue
				}
				return stopErr(ctx, err)
			} else if outcome == toolBlocked || outcome == toolCancelled {
				return nil
			}
			a.persistMissionProgress()
			a.compactBestEffort(ctx)
			// MVP-11 safety: infer one tool call once only.
			wantsTool = false
			continue
		}

		a.injectCompletedBackgroundTasks()

		reply, nativeCalls, err := a.chatOnce(ctx)
		if err != nil {
			if IsThinkingLoopLimitErr(err) {
				emptyChatErrRetries = 0
				thinkingLoopStops++
				if thinkingLoopStops >= maxThinkingLoopStops {
					return thinkingLoopStopErr(thinkingLoopStops)
				}
				a.sess.AddRuntimeReminder("Thinking loop error: The thinking process was interrupted because a cyclical reasoning loop was detected 3 consecutive times in this step. Please rethink the task using an alternative approach or different strategy.")
				a.compactBestEffort(ctx)
				continue
			}
			if IsEmptyAssistantResponseErr(err) {
				done, err := a.handleEmptyChatResponse(ctx, &emptyChatErrRetries)
				if done {
					return err
				}
				continue
			}
			return err
		}
		emptyChatErrRetries = 0
		// The provider stream answers a cancelled request with a marker reply.
		// Counting it as visible text is what let a pending checklist send the
		// loop back for another iteration instead of stopping the turn.
		if ctx.Err() != nil || strings.TrimSpace(reply) == cancelledReply {
			return turnCancelledErr(ctx.Err())
		}
		if len(nativeCalls) > 0 {
			// Native function calling wins over the XML envelope: the
			// model already chose exact tools, so execute them directly.
			thinkingOnlyRetries = 0
			if len(nativeCalls) > MaxToolsPerReply {
				nativeCalls = nativeCalls[:MaxToolsPerReply]
			}
			if strings.TrimSpace(reply) == "" {
				a.ui.CollapseAssistantOutput()
			}
			// Rewrite hallucinated or mis-cased names to the registry's
			// canonical spelling before they reach history; names that
			// still do not resolve keep their original text and get an
			// error result inside the batch below.
			for i := range nativeCalls {
				if resolved, ok := a.resolveToolName(nativeCalls[i].Name); ok {
					nativeCalls[i].Name = resolved
				}
			}
			// Persist the structured calls (and any visible text) so the
			// next turn replays Ollama's native history shape instead of
			// a text-only approximation that loses arguments.
			a.sess.AddAssistantToolCalls(reply, toolCallsToWire(nativeCalls))
			var auxNotes []func()
			var batchErr error
			batchHalted := false
			unknownCount := 0
			var unknownNames []string
			seenUnknown := map[string]bool{}
			for i, c := range nativeCalls {
				tool := a.reg.Get(c.Name)
				if tool == nil {
					unknownCount++
					if !seenUnknown[c.Name] {
						seenUnknown[c.Name] = true
						unknownNames = append(unknownNames, c.Name)
					}
					msg := a.unknownToolMessage(c.Name)
					a.ui.ShowToolResult(c.Name, msg, true, c.Params)
					a.sess.AddToolResult(c.Name, msg)
					continue
				}
				_, outcome, err := a.executeTool(ctx, tool, c.Name, c.Params, toolExecutionMode{
					showPermissionDeniedResult: true,
					structured:                 true,
					auxiliaryNotes:             &auxNotes,
				})
				if err != nil {
					batchErr = err
					for j := i + 1; j < len(nativeCalls); j++ {
						a.sess.AddToolResult(nativeCalls[j].Name, "Tool execution skipped: previous tool call in batch failed.")
					}
					break
				}
				if outcome == toolCancelled || outcome == toolBlocked {
					// Cancel ends the run; a policy block ends the turn. A plain
					// denial does neither: that one call is recorded as refused
					// and the batch keeps going, so the model still has its
					// results to work with.
					batchHalted = true
					cancelled := outcome == toolCancelled
					for j := i + 1; j < len(nativeCalls); j++ {
						if cancelled {
							a.sess.AddToolResult(nativeCalls[j].Name, "Tool execution skipped: the user cancelled this run.")
							continue
						}
						a.sess.AddToolResult(nativeCalls[j].Name, "Tool execution skipped: previous tool call in batch was blocked by policy.")
					}
					break
				}
				a.persistMissionProgress()
			}
			for _, fn := range auxNotes {
				fn()
			}
			// A batch where every call was unknown means the model is
			// inventing tool names; count consecutive rounds so a stuck
			// model stops with a resumable error instead of churning
			// until the iteration budget runs out.
			if unknownCount == len(nativeCalls) {
				unknownToolRounds++
			} else {
				unknownToolRounds = 0
			}
			if unknownToolRounds > maxUnknownToolRounds {
				return fmt.Errorf("the model kept calling tools that do not exist (%s). Nothing was lost: press Enter to continue; type a correction instead, or tell the model which tool to use", strings.Join(unknownNames, ", "))
			}
			if batchErr != nil {
				if a.recoverNoProgress(ctx, batchErr, &noProgressRetries) {
					continue
				}
				return stopErr(ctx, batchErr)
			}
			if batchHalted {
				return nil
			}
			a.compactBestEffort(ctx)
			continue
		}
		if calls := parseXMLFallbackAll(reply, MaxToolsPerReply); len(calls) > 0 {
			thinkingOnlyRetries = 0
			// Resolve hallucinated or mis-cased envelope names to canonical
			// spellings before looking them up; names that still do not
			// resolve are answered with an error observation below.
			for i := range calls {
				if resolved, ok := a.resolveToolName(calls[i].Name); ok {
					calls[i].Name = resolved
				}
			}
			unknownCount := 0
			for _, c := range calls {
				if a.reg.Get(c.Name) == nil {
					unknownCount++
				}
			}
			if unknownCount > 0 && assistantVisibleText(reply) != "" {
				// Prose plus an unknown envelope is a final answer: the
				// model already said what it had to say, so keep the turn
				// ending on the reply instead of a partial execution.
				a.sess.AddAssistant(reply)
				a.compactBestEffort(ctx)
				return nil
			}
			if assistantVisibleText(reply) == "" {
				a.ui.CollapseAssistantOutput()
			}
			a.sess.AddAssistant(reply)
			var auxNotes []func()
			var unknownNames []string
			seenUnknown := map[string]bool{}
			for _, c := range calls {
				tool := a.reg.Get(c.Name)
				if tool == nil {
					if !seenUnknown[c.Name] {
						seenUnknown[c.Name] = true
						unknownNames = append(unknownNames, c.Name)
					}
					a.sess.AddToolObservation(c.Name, a.unknownToolMessage(c.Name))
					continue
				}
				_, outcome, err := a.executeTool(ctx, tool, c.Name, c.Params, toolExecutionMode{
					showPermissionDeniedResult: true,
					auxiliaryNotes:             &auxNotes,
				})
				if err != nil {
					for _, fn := range auxNotes {
						fn()
					}
					if a.recoverNoProgress(ctx, err, &noProgressRetries) {
						auxNotes = nil
						break
					}
					return stopErr(ctx, err)
				}
				if outcome == toolBlocked || outcome == toolCancelled {
					for _, fn := range auxNotes {
						fn()
					}
					return nil
				}
				a.persistMissionProgress()
			}
			for _, fn := range auxNotes {
				fn()
			}
			// Same unknown-name accounting as the native path: only a
			// batch made entirely of unresolved calls counts toward the
			// cap, and any known call resets the streak.
			if unknownCount == len(calls) {
				unknownToolRounds++
			} else {
				unknownToolRounds = 0
			}
			if unknownToolRounds > maxUnknownToolRounds {
				return fmt.Errorf("the model kept calling tools that do not exist (%s). Nothing was lost: press Enter to continue; type a correction instead, or tell the model which tool to use", strings.Join(unknownNames, ", "))
			}
			a.compactBestEffort(ctx)
			continue
		}
		if assistantVisibleText(reply) == "" {
			if thinkingOnlyRetries < 1 {
				thinkingOnlyRetries++
				// Retrying with an identical prompt asks the same question that
				// just got no answer. The reminder makes the retry a different
				// request and names what was missing.
				a.sess.AddRuntimeReminder("Your previous reply contained no visible answer and no tool call (only reasoning, or nothing). Reply to the user in plain text, or call one concrete tool now.")
				a.compactBestEffort(ctx)
				continue
			}
			thinkingOnlyRetries = 0
			if a.hasPendingTodos() {
				a.sess.AddRuntimeReminder("Model reply was empty while TODO items remain. Continue with the next pending step via a tool call; do not re-investigate completed steps.")
				a.compactBestEffort(ctx)
				continue
			}
			// A turn that ends here would show the user nothing at all: no
			// answer, no footer, no error. Report the empty reply instead of
			// returning success so the caller can offer a retry.
			a.sess.AddSystemNote("Model reply was empty; ending this run without a visible assistant message.")
			a.compactBestEffort(ctx)
			return emptyAssistantResponseErr()
		}
		thinkingOnlyRetries = 0
		if a.hasPendingTodos() {
			a.sess.AddAssistant(reply)
			// A checklist that does not move while the answer repeats is a
			// stall no other guard can see: there is no tool call to compare
			// and the reply is not empty. Name the repetition, point at the
			// next item, then stop instead of asking up to MaxIterations times.
			streak := a.replyRepeat.observe(reply)
			if streak > maxRepeatedReplies {
				a.compactBestEffort(ctx)
				return repeatedReplyErr(streak, a.nextPendingTodo())
			}
			if streak > 1 {
				a.sess.AddRuntimeReminder(a.repeatedReplyNote(reply))
			} else if next := a.nextPendingTodo(); next != "" {
				a.sess.AddRuntimeReminder("There are still pending TODO items. Execute the next item now with a concrete change (Write, Edit, or a mutating Bash command) — a further Read, Glob, or Grep is not executing it: " + next)
			} else {
				a.sess.AddRuntimeReminder("There are still pending TODO items. Continue executing the remaining steps with tool calls — do not finish the turn with plain text.")
			}
			a.compactBestEffort(ctx)
			continue
		}
		a.sess.AddAssistant(reply)
		a.compactBestEffort(ctx)
		return nil
	}
	return fmt.Errorf("iteration cap reached (%d): this turn used all of its steps without finishing. Nothing was lost: press Enter to continue; type a correction instead from where it stopped", MaxIterations)
}
