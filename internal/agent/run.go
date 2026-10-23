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

func (a *Agent) Run(rootCtx context.Context, userInput string) error {
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
	// Persist the checklist/task state at the end of every turn so a crash or
	// restart never loses what is done and what remains.
	defer a.PersistWorkState()

	a.resetTurnToolCalls()
	// The repeated-call budget is per turn: a streak left by an earlier request
	// must not abort this one on its first identical call.
	a.noProgress.reset()
	// Keep permission mode in sync with the mission at the start of every turn
	// so unattended approval can never leak into a non-mission run.
	if a.perm != nil {
		a.perm.SetUnattended(a.MissionActive())
	}
	a.addRunContext(ctx, userInput)

	// ParallelAgents is explicit-only: the model calls it when a task is
	// genuinely decomposable. Previous auto-detection split any message
	// containing " and " or numbered lines, hijacking normal requests.
	toolName, toolParams, wantsTool := inferSingleToolCall(userInput)
	thinkingOnlyRetries := 0
	emptyChatErrRetries := 0
	noProgressRetries := 0
	for i := 0; i < MaxIterations; i++ {
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
				return noAnswerStopErr(err)
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
		if len(nativeCalls) > 0 {
			// Native function calling wins over the XML envelope: the
			// model already chose exact tools, so execute them directly.
			// Unknown names fall through to the XML path below, which
			// treats the turn as a final answer instead of partial work.
			known := true
			for _, c := range nativeCalls {
				if a.reg.Get(c.Name) == nil {
					known = false
					break
				}
			}
			if known {
				thinkingOnlyRetries = 0
				if len(nativeCalls) > MaxToolsPerReply {
					nativeCalls = nativeCalls[:MaxToolsPerReply]
				}
				if strings.TrimSpace(reply) == "" {
					a.ui.CollapseAssistantOutput()
				}
				// Persist the structured calls (and any visible text) so the
				// next turn replays Ollama's native history shape instead of
				// a text-only approximation that loses arguments.
				a.sess.AddAssistantToolCalls(reply, toolCallsToWire(nativeCalls))
				var auxNotes []func()
				var batchErr error
				batchHalted := false
				for i, c := range nativeCalls {
					tool := a.reg.Get(c.Name)
					if tool == nil {
						msg := fmt.Sprintf("Error: tool %q is not available.", c.Name)
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
					if isMissionEndTool(c.Name) {
						for _, fn := range auxNotes {
							fn()
						}
						return nil
					}
				}
				for _, fn := range auxNotes {
					fn()
				}
				if batchErr != nil {
					if a.recoverNoProgress(ctx, batchErr, &noProgressRetries) {
						continue
					}
					return noAnswerStopErr(batchErr)
				}
				if batchHalted {
					return nil
				}
				a.compactBestEffort(ctx)
				continue
			}
		}
		if calls := parseXMLFallbackAll(reply, MaxToolsPerReply); len(calls) > 0 {
			thinkingOnlyRetries = 0
			// Verify every tool exists before executing any, so a reply
			// mixing known and unknown envelopes falls back to a final
			// answer instead of a confusing partial execution.
			unknown := ""
			for _, c := range calls {
				if a.reg.Get(c.Name) == nil {
					unknown = c.Name
					break
				}
			}
			if unknown != "" {
				a.sess.AddAssistant(reply)
				a.compactBestEffort(ctx)
				if assistantVisibleText(reply) == "" {
					// An unknown tool envelope alone is not an answer: the user
					// would see the turn end with no text at all.
					return fmt.Errorf("model called unknown tool %q and produced no visible answer", unknown)
				}
				return nil
			}
			if assistantVisibleText(reply) == "" {
				a.ui.CollapseAssistantOutput()
			}
			a.sess.AddAssistant(reply)
			for _, c := range calls {
				tool := a.reg.Get(c.Name)
				_, outcome, err := a.executeTool(ctx, tool, c.Name, c.Params, toolExecutionMode{
					showPermissionDeniedResult: true,
				})
				if err != nil {
					if a.recoverNoProgress(ctx, err, &noProgressRetries) {
						break
					}
					return noAnswerStopErr(err)
				}
				if outcome == toolBlocked || outcome == toolCancelled {
					return nil
				}
				a.persistMissionProgress()
				if isMissionEndTool(c.Name) {
					return nil
				}
			}
			a.compactBestEffort(ctx)
			continue
		}
		if assistantVisibleText(reply) == "" {
			if thinkingOnlyRetries < 2 {
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
			a.sess.AddRuntimeReminder("There are still pending TODO items. Continue executing the remaining steps with tool calls — do not finish the turn with plain text.")
			a.compactBestEffort(ctx)
			continue
		}
		a.sess.AddAssistant(reply)
		a.compactBestEffort(ctx)
		return nil
	}
	return fmt.Errorf("iteration cap reached (%d): this turn used all of its steps without finishing. Nothing was lost: send \"continue\" to resume from where it stopped", MaxIterations)
}
